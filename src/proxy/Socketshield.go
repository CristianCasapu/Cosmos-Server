package proxy

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/constellation"
)

// TCPSmartShield: same budgets and bans as the HTTP shield, per connection.
// A connection's bytes and packets accumulate on its wrapper while it is
// open and fold into the client's rolling window when it closes; the budget
// enforcer runs every 10s to throttle or kick clients that went over.

type TCPConnectionWrapper struct {
	Conn          net.Conn
	TimeStarted   time.Time
	TimeEnded     time.Time
	ClientID      string
	Packets       int64
	Bytes         int64
	IsOver        bool
	ShieldID      string
	Policy        utils.SmartShieldPolicy
	IsPrivileged  bool
	throttleNs    atomic.Int64
	mutex         sync.Mutex
	Route         utils.ProxyRouteConfig
	budget        *clientBudget
	closeOnce     sync.Once
}

// Implement the missing methods to satisfy the net.Conn interface
func (w *TCPConnectionWrapper) LocalAddr() net.Addr {
	return w.Conn.LocalAddr()
}

func (w *TCPConnectionWrapper) RemoteAddr() net.Addr {
	return w.Conn.RemoteAddr()
}

func (w *TCPConnectionWrapper) SetDeadline(t time.Time) error {
	return w.Conn.SetDeadline(t)
}

func (w *TCPConnectionWrapper) SetReadDeadline(t time.Time) error {
	return w.Conn.SetReadDeadline(t)
}

func (w *TCPConnectionWrapper) SetWriteDeadline(t time.Time) error {
	return w.Conn.SetWriteDeadline(t)
}

func (w *TCPConnectionWrapper) counters() (int64, int64) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.Bytes, w.Packets
}

var socketShield = newBudgetStore()

func (s *budgetStore) GetServerConnections(shieldID string) int {
	return int(s.shieldInflight(shieldID).Load())
}

func CleanUpSocket() {
	removed := socketShield.cleanup(time.Now())
	utils.Log("SmartShield: Cleaned up " + fmt.Sprintf("%d", removed) + " idle socket clients")
}

// IsAllowedToConnect checks bans, then strikes the client if its usage is
// beyond the policy times the strictness factor.
func IsAllowedToConnect(shieldID string, policy utils.SmartShieldPolicy, userConsumed userUsedBudget) bool {
	if !policy.Enabled {
		return true
	}

	clientID := userConsumed.ClientID
	if isLocalGateway(clientID) {
		return true
	}

	now := time.Now()
	if !globalShieldState.allowed(clientID, shieldID, now) {
		return false
	}

	if reason, over := budgetViolation(policy, userConsumed, shieldID, true); over {
		globalShieldState.strike(clientID, shieldID, reason, now)
		return false
	}

	utils.Debug(fmt.Sprintf("TCPSmartShield: User %s is allowed to connect", clientID))
	return true
}

func TCPSmartShieldWrapper(conn net.Conn, shieldID string, route utils.ProxyRouteConfig, policy utils.SmartShieldPolicy) *TCPConnectionWrapper {
	clientID, _, _ := net.SplitHostPort(conn.RemoteAddr().String())

	wrapper := &TCPConnectionWrapper{
		Conn:         conn,
		TimeStarted:  time.Now(),
		ClientID:     clientID,
		ShieldID:     shieldID,
		Policy:       policy,
		Route:        route,
		IsPrivileged: utils.IsShieldWhitelisted(clientID),
		budget:       socketShield.client(shieldID, clientID),
	}

	wrapper.budget.Lock()
	if wrapper.budget.live == nil {
		wrapper.budget.live = map[*TCPConnectionWrapper]struct{}{}
	}
	wrapper.budget.live[wrapper] = struct{}{}
	wrapper.budget.lastSeen = wrapper.TimeStarted
	wrapper.budget.Unlock()
	socketShield.shieldInflight(shieldID).Add(1)

	utils.TriggerEvent(
		"cosmos.socket-proxy.opened." + wrapper.Route.Name,
		"Socket Proxy " + wrapper.Route.Name + " Opened for " + wrapper.ClientID,
		"success",
		"route@" + wrapper.Route.Name,
		map[string]interface{}{
		"clientID": wrapper.ClientID,
	})

	return wrapper
}

func (w *TCPConnectionWrapper) throttle() {
	if ns := w.throttleNs.Load(); ns > 0 {
		time.Sleep(time.Duration(ns))
	}
}

func (w *TCPConnectionWrapper) Write(b []byte) (int, error) {
	w.throttle()

	n, err := w.Conn.Write(b)

	w.mutex.Lock()
	w.Bytes += int64(n)
	w.Packets++
	w.mutex.Unlock()

	return n, err
}

func (w *TCPConnectionWrapper) Read(b []byte) (int, error) {
	w.throttle()

	n, err := w.Conn.Read(b)

	w.mutex.Lock()
	w.Bytes += int64(n)
	w.Packets++
	w.mutex.Unlock()

	return n, err
}

// EnforceBudget throttles every live connection of a client that is over
// its policy, and kicks the client (one strike, all its connections) when it
// is over by the strictness factor.
func (s *budgetStore) EnforceBudget() {
	now := time.Now()
	s.each(func(b *clientBudget) {
		b.Lock()
		conns := make([]*TCPConnectionWrapper, 0, len(b.live))
		for conn := range b.live {
			conns = append(conns, conn)
		}
		b.Unlock()
		if len(conns) == 0 {
			return
		}

		// one policy per shield/client pair: every live conn carries the same one
		policy := conns[0].Policy
		clientID := conns[0].ClientID
		if !policy.Enabled || conns[0].IsPrivileged {
			return
		}

		userConsumed := b.consumed(clientID, now)

		throttle := 0

		overReq := int64(policy.PerUserRequestLimit*1000) - userConsumed.Packets
		overReqRatio := float64(overReq) / float64(policy.PerUserRequestLimit*1000)
		if overReq < 0 {
			newThrottle := int(float64(300) * -overReqRatio)
			if newThrottle > throttle {
				throttle = newThrottle
			}
		}

		overByte := policy.PerUserByteLimit - userConsumed.Bytes
		overByteRatio := float64(overByte) / float64(policy.PerUserByteLimit)
		if overByte < 0 {
			newThrottle := int(float64(40) * -overByteRatio)
			if newThrottle > throttle {
				throttle = newThrottle
			}
		}

		overSim := policy.PerUserSimultaneous - userConsumed.Simultaneous
		overSimRatio := float64(overSim) / float64(policy.PerUserSimultaneous)
		if overSim < 0 {
			newThrottle := int(float64(50) * -overSimRatio)
			if newThrottle > throttle {
				throttle = newThrottle
			}
		}

		for _, conn := range conns {
			conn.throttleNs.Store(int64(time.Duration(throttle) * time.Millisecond))
		}

		// live connections are not kicked for being many, only for what they moved
		usage := userConsumed
		usage.Simultaneous = 0
		reason, over := budgetViolation(policy, usage, conns[0].ShieldID, true)
		if !over {
			return
		}

		utils.Warn(fmt.Sprintf("TCPSmartShield: Kicking out user %s due to exceeded budget", clientID))
		globalShieldState.strike(clientID, conns[0].ShieldID, reason, now)

		for _, conn := range conns {
			conn.Close()
			utils.TriggerEvent(
				"cosmos.proxy.shield.abuse." + conn.Route.Name,
				"Socket Shield " + conn.Route.Name + " Abuse by " + conn.ClientID,
				"warning",
				"route@" + conn.Route.Name,
				map[string]interface{}{
				"route": conn.Route.Name,
				"consumed": userConsumed,
				"lastBan": GetLastBan(conn.ClientID),
				"clientID": conn.ClientID,
			})
		}
	})
}

func StartBudgetEnforcer() {
	go func() {
		for {
			time.Sleep(10 * time.Second)
			socketShield.EnforceBudget()
		}
	}()
}

func IPInRange(ip, cidr string) (bool, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false, err
	}
	parsedIP := net.ParseIP(ip)
	return ipnet.Contains(parsedIP), nil
}

func (w *TCPConnectionWrapper) Close() error {
	w.closeOnce.Do(func() {
		w.TimeEnded = time.Now()
		w.IsOver = true

		bytes, packets := w.counters()
		if w.budget != nil {
			w.budget.Lock()
			delete(w.budget.live, w)
			slot := w.budget.slot(w.TimeEnded)
			slot.bytes += bytes
			slot.packets += packets
			slot.seconds += w.TimeEnded.Sub(w.TimeStarted).Seconds()
			w.budget.lastSeen = w.TimeEnded
			w.budget.Unlock()
			socketShield.shieldInflight(w.ShieldID).Add(-1)
		}

		utils.TriggerEvent(
			"cosmos.socket-proxy.closed." + w.Route.Name,
			"Socket Proxy " + w.Route.Name + " Closed for " + w.ClientID,
			"success",
			"route@" + w.Route.Name,
			map[string]interface{}{
			"route": w.Route.Name,
			"consumed": bytes,
			"packets": packets,
			"time": w.TimeEnded.Sub(w.TimeStarted).Seconds(),
			"clientID": w.ClientID,
		})

		go metrics.PushRequestMetrics(w.Route, 0, w.TimeStarted, bytes)
	})

	return w.Conn.Close()
}

func InitSocketShield() {
	StartBudgetEnforcer()
}

func TCPSmartShieldMiddleware(shieldID string, route utils.ProxyRouteConfig) func(net.Conn) net.Conn {
	policy := utils.ApplySmartShieldDefaults(route.SmartShield)

	return func(conn net.Conn) net.Conn {
		clientID, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
		shieldEntry, shieldWhitelisted := utils.ShieldWhitelistMatch(clientID)

		if !shieldWhitelisted && utils.GetIPAbuseCounter(clientID) > 275 {
			conn.Close()
			return nil
		}

		whitelistInboundIPs := route.WhitelistInboundIPs
		restrictToConstellation := route.RestrictToConstellation

		// Whitelist / Constellation check

		isUsingWhitelist := len(whitelistInboundIPs) > 0
		isInWhitelist := shieldWhitelisted && shieldEntry.BypassIPRestriction
		isInConstellation := constellation.IsConstellationIP(clientID)
		// a local peer's packets never crossed Nebula but satisfy restrictToConstellation too
		isLocalPeer := utils.IsLocalPeer(clientID)

		for _, ipRange := range whitelistInboundIPs {
			utils.Debug(fmt.Sprintf("Checking if %s is in %s", clientID, ipRange))
			if strings.Contains(ipRange, "/") {
				if ok, _ := IPInRange(clientID, ipRange); ok {
					isInWhitelist = true
					break
				}
			} else if clientID == ipRange {
				isInWhitelist = true
				break
			}
		}

		if restrictToConstellation && !isInConstellation && !isLocalPeer {
			if !isUsingWhitelist || (isUsingWhitelist && !isInWhitelist) {
				utils.PushShieldMetrics("ip-whitelists")
				utils.TriggerEvent(
					"cosmos.proxy.shield.whitelist",
					"Socket Shield IP blocked by whitelist",
					"warning",
					"",
					map[string]interface{}{
						"clientID": clientID,
					},
				)
				utils.IncrementIPAbuseCounter(clientID)
				utils.Error(fmt.Sprintf("Connection from %s is blocked because of restrictions", clientID), nil)
				utils.Debug("Blocked by RestrictToConstellation isInConstellation isUsingWhitelist isInWhitelist")
				conn.Close()
				return nil
			}
		} else if isUsingWhitelist && !isInWhitelist {
			utils.PushShieldMetrics("ip-whitelists")
			utils.TriggerEvent(
				"cosmos.proxy.shield.whitelist",
				"Socket Shield IP blocked by whitelist",
				"warning",
				"",
				map[string]interface{}{
					"clientID": clientID,
				},
			)
			utils.IncrementIPAbuseCounter(clientID)
			utils.Error(fmt.Sprintf("Connection from %s is blocked because of restrictions", clientID), nil)
			utils.Debug("Blocked by isUsingWhitelist isInWhitelist")
			conn.Close()
			return nil
		}

		// Geo check
		
		countryCode, err := utils.GetIPLocation(clientID)
		if shieldWhitelisted && shieldEntry.BypassGeo {
			err = fmt.Errorf("geo check bypassed by shield whitelist")
		}
		if err == nil {
			config := utils.GetMainConfig()
			countryBlacklistIsWhitelist := config.CountryBlacklistIsWhitelist
			blockedCountries := config.BlockedCountries

			if countryBlacklistIsWhitelist {
				if countryCode != "" {
					utils.Debug("Country code: " + countryCode)
					blocked := true
					for _, blockedCountry := range blockedCountries {
						if config.ServerCountry != countryCode && countryCode == blockedCountry {
							blocked = false
						}
					}

					if blocked {
						utils.PushShieldMetrics("geo")
						utils.IncrementIPAbuseCounter(clientID)

						utils.TriggerEvent(
							"cosmos.proxy.shield.geo",
							"Proxy Shield Geo blocked",
							"warning",
							"",
							map[string]interface{}{
							"clientID": clientID,
							"country": countryCode,
							"route": route.Name,
						})

						utils.Warn(fmt.Sprintf("Connection from %s is blocked because of geo restrictions", clientID))
						conn.Close()
						return nil
					}
				} else {
					utils.Debug("Missing geolocation information to block IPs")
				}
			} else {
				for _, blockedCountry := range blockedCountries {
					if config.ServerCountry != countryCode && countryCode == blockedCountry {

						utils.PushShieldMetrics("geo")
						utils.IncrementIPAbuseCounter(clientID)

						utils.TriggerEvent(
							"cosmos.proxy.shield.geo",
							"Proxy Shield Geo blocked",
							"warning",
							"",
							map[string]interface{}{
							"clientID": clientID,
							"country": countryCode,
							"route": route.Name,
						})

						utils.Warn(fmt.Sprintf("Connection from %s is blocked because of geo restrictions", clientID))

						conn.Close()
						return nil
					}
				}
			}
		} else {
			utils.Debug("Missing geolocation information to block IPs")
		}
		
		userConsumed := socketShield.GetUserUsedBudgets(shieldID, clientID)

		if !shieldWhitelisted && !IsAllowedToConnect(shieldID, policy, userConsumed) {
			utils.TriggerEvent(
				"cosmos.proxy.shield.abuse." + route.Name,
				"Socket Shield " + route.Name + " Abuse by " + clientID,
				"warning",
				"route@" + route.Name,
				map[string]interface{}{
				"route": route.Name,
				"consumed": userConsumed,
				"lastBan": GetLastBan(clientID),
				"clientID": clientID,
			})

			utils.Warn(fmt.Sprintf("TCPSmartShield: Connection blocked for %s due to policy violation", clientID))
			conn.Close()
			return nil
		}

		wrapper := TCPSmartShieldWrapper(conn, shieldID, route, policy)

		if policy.Enabled && !shieldWhitelisted {
			currentConnections := socketShield.GetServerConnections(shieldID)
			utils.Debug(fmt.Sprintf("TCPSmartShield: Current global connections: %d", currentConnections))
	
			if currentConnections > policy.MaxGlobalSimultaneous {
				utils.TriggerEvent(
					"cosmos.proxy.shield.abuse." + route.Name,
					"Socket Shield " + route.Name + " Abuse by " + clientID,
					"warning",
					"route@" + route.Name,
					map[string]interface{}{
					"route": route.Name,
					"consumed": userConsumed,
					"clientID": clientID,
				})

				utils.Warn(fmt.Sprintf("TCPSmartShield: Too many connections on the server for %s (%d of %d)", shieldID, currentConnections, policy.MaxGlobalSimultaneous))
				wrapper.Close()
				return nil
			}
		}

		return wrapper
	}
}