package proxy

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/constellation"
	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/utils"
)

// SmartShield (HTTP): per-client budgets over a rolling hour, throttling when a
// budget is exceeded and strikes (see shield_bans.go) when it is exceeded by
// the policy's strictness factor. Budgets are aggregate counters
// (shield_budget.go), so cost per request does not grow with traffic.
//
// Identity: an authenticated request is budgeted per Cosmos user, an anonymous
// one per source IP. Abuse counters and IP-level defences stay keyed by IP.

type userUsedBudget struct {
	ClientID     string  `json:"clientID"`
	Time         float64 `json:"time"` // seconds
	Requests     int     `json:"requests"`
	Packets      int64   `json:"packets,omitempty"`
	Bytes        int64   `json:"bytes"`
	Simultaneous int     `json:"simultaneous"`
}

var shield = newBudgetStore()

func GetShield() int {
	return shield.size() + globalShieldState.count()
}

func CleanUp() {
	now := time.Now()
	removed := shield.cleanup(now) + globalShieldState.cleanup(now)
	utils.Log("SmartShield: Cleaned up " + fmt.Sprintf("%d", removed) + " items")
}

// consumed reads a client's usage over the window, in-flight work included.
func (b *clientBudget) consumed(clientID string, now time.Time) userUsedBudget {
	b.Lock()
	defer b.Unlock()

	f := b.finished(now)
	u := userUsedBudget{
		ClientID:     clientID,
		Time:         f.seconds,
		Requests:     f.requests,
		Packets:      f.packets,
		Bytes:        f.bytes,
		Simultaneous: b.inflight + len(b.live),
	}
	if b.inflight > 0 {
		u.Time += float64(int64(b.inflight)*now.UnixNano()-b.inflightStartNs) / 1e9
	}
	for w := range b.live {
		bytes, packets := w.counters()
		u.Bytes += bytes
		u.Packets += packets
		u.Time += now.Sub(w.TimeStarted).Seconds()
	}
	return u
}

func (s *budgetStore) GetUserUsedBudgets(shieldID string, clientID string) userUsedBudget {
	return s.client(shieldID, clientID).consumed(clientID, time.Now())
}

func isLocalGateway(clientID string) bool {
	return clientID == "192.168.1.1" ||
		clientID == "192.168.0.1" ||
		clientID == "192.168.0.254" ||
		clientID == "172.17.0.1"
}

func overTimeBudget(policy utils.SmartShieldPolicy, seconds float64) bool {
	if policy.PerUserTimeBudget <= 0 {
		return false
	}
	return seconds*1000 > policy.PerUserTimeBudget*float64(policy.PolicyStrictness)
}

// budgetViolation names the first limit a client is over by the policy's
// strictness factor. HTTP tolerates 15x the simultaneous limit before a
// strike (browsers open many connections); TCP does not.
func budgetViolation(policy utils.SmartShieldPolicy, u userUsedBudget, route string, tcp bool) (utils.ShieldBanReason, bool) {
	strictness := policy.PolicyStrictness
	reason := utils.ShieldBanReason{Route: route}
	switch {
	case overTimeBudget(policy, u.Time):
		reason.Limit, reason.Used, reason.Allowed = "time", u.Time*1000, policy.PerUserTimeBudget*float64(strictness)
	case !tcp && u.Requests > policy.PerUserRequestLimit*strictness:
		reason.Limit, reason.Used, reason.Allowed = "requests", float64(u.Requests), float64(policy.PerUserRequestLimit*strictness)
	case tcp && u.Packets > int64(policy.PerUserRequestLimit*1000*strictness):
		reason.Limit, reason.Used, reason.Allowed = "packets", float64(u.Packets), float64(policy.PerUserRequestLimit*1000*strictness)
	case u.Bytes > policy.PerUserByteLimit*int64(strictness):
		reason.Limit, reason.Used, reason.Allowed = "bytes", float64(u.Bytes), float64(policy.PerUserByteLimit*int64(strictness))
	case !tcp && u.Simultaneous > policy.PerUserSimultaneous*strictness*15:
		reason.Limit, reason.Used, reason.Allowed = "simultaneous", float64(u.Simultaneous), float64(policy.PerUserSimultaneous*strictness*15)
	case tcp && u.Simultaneous > policy.PerUserSimultaneous*strictness:
		reason.Limit, reason.Used, reason.Allowed = "simultaneous", float64(u.Simultaneous), float64(policy.PerUserSimultaneous*strictness)
	default:
		return reason, false
	}
	return reason, true
}

// isAllowedToRequest checks bans first, then strikes the client if its usage
// is beyond the policy times the strictness factor.
func isAllowedToRequest(shieldID string, policy utils.SmartShieldPolicy, userConsumed userUsedBudget) bool {
	clientID := userConsumed.ClientID
	if isLocalGateway(clientID) {
		return true
	}

	now := time.Now()
	if !globalShieldState.allowed(clientID, shieldID, now) {
		return false
	}

	if reason, over := budgetViolation(policy, userConsumed, shieldID, false); over {
		globalShieldState.strike(clientID, shieldID, reason, now)
		return false
	}

	return true
}

func computeThrottle(policy utils.SmartShieldPolicy, userConsumed userUsedBudget) int {
	throttle := 0

	overReq := policy.PerUserRequestLimit - userConsumed.Requests
	overReqRatio := float64(overReq) / float64(policy.PerUserRequestLimit)
	if overReq < 0 {
		newThrottle := int(float64(2500) * -overReqRatio)
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

	if throttle > 0 {
		utils.Debug(fmt.Sprintf("SmartShield: throttling %s by %dms (requests %d/%d, bytes %d/%d, simultaneous %d/%d)",
			userConsumed.ClientID, throttle,
			userConsumed.Requests, policy.PerUserRequestLimit,
			userConsumed.Bytes, policy.PerUserByteLimit,
			userConsumed.Simultaneous, policy.PerUserSimultaneous))
	}

	return throttle
}

func calculateLowestExhaustedPercentage(policy utils.SmartShieldPolicy, userConsumed userUsedBudget) int64 {
	lowest := 100.0
	if policy.PerUserTimeBudget > 0 {
		lowest = math.Min(lowest, 100-(userConsumed.Time*1000/policy.PerUserTimeBudget)*100)
	}
	lowest = math.Min(lowest, 100-(float64(userConsumed.Requests)/float64(policy.PerUserRequestLimit))*100)
	lowest = math.Min(lowest, 100-(float64(userConsumed.Bytes)/float64(policy.PerUserByteLimit))*100)
	return int64(math.Max(0, lowest))
}

// GetClientID returns the source IP of a request, honouring X-Forwarded-For
// from trusted proxies and authenticated constellation peers.
func GetClientID(r *http.Request, route utils.ProxyRouteConfig) string {
	remoteAddr, _ := utils.SplitIP(r.RemoteAddr)
	isConstIP := constellation.IsConstellationIP(remoteAddr)
	isConstTokenValid := isConstIP && constellation.CheckConstellationToken(r) == nil

	if utils.IsTrustedProxy(remoteAddr) || isConstTokenValid {
		if ip, _ := utils.SplitIP(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])); ip != "" {
			utils.Debug("SmartShield: Getting forwarded client ID " + ip)
			return ip
		}
	}

	// no usable forwarded address: fall back to the peer IP
	ip, _ := utils.SplitIP(r.RemoteAddr)
	utils.Debug("SmartShield: Getting client ID " + ip)
	return ip
}

// GetShieldIdentity is what budgets and bans are keyed on: the Cosmos user
// when the request is authenticated, otherwise the source IP. Many users
// behind one NAT then no longer share one budget.
func GetShieldIdentity(r *http.Request, clientIP string) string {
	if nickname := utils.GetAuthContext(r).Nickname; nickname != "" {
		return "user:" + nickname
	}
	return clientIP
}

func isPrivileged(req *http.Request, policy utils.SmartShieldPolicy) bool {
	switch {
	case policy.PrivilegedGroups <= 0:
		return true
	case policy.PrivilegedGroups == 1:
		return utils.HasPermission(req, utils.PERM_LOGIN)
	default:
		return utils.HasPermission(req, utils.PERM_ADMIN_READ)
	}
}

func finishRequest(wrapper *SmartResponseWriterWrapper, route utils.ProxyRouteConfig, r *http.Request) {
	wrapper.TimeEnded = time.Now()
	wrapper.isOver = true

	statusText := "success"
	level := "info"
	if wrapper.Status >= 400 {
		statusText = "error"
		level = "warning"
	}

	utils.TriggerEvent(
		"cosmos.proxy.response."+route.Name+"."+statusText,
		"Proxy Response "+route.Name+" "+statusText,
		level,
		"route@"+route.Name,
		map[string]interface{}{
			"route":    route.Name,
			"status":   wrapper.Status,
			"method":   wrapper.Method,
			"clientID": wrapper.ClientIP,
			"identity": wrapper.ClientID,
			"time":     wrapper.TimeEnded.Sub(wrapper.TimeStarted).Seconds(),
			"bytes":    wrapper.Bytes,
			"hostname": r.Host,
			"url":      r.URL,
		})

	go metrics.PushRequestMetrics(route, wrapper.Status, wrapper.TimeStarted, wrapper.Bytes)
}

func rejectTooManyRequests(w http.ResponseWriter, reason string) {
	go metrics.PushShieldMetrics("smart-shield")
	utils.Log("SmartShield: " + reason)
	http.Error(w, "Too many requests", http.StatusTooManyRequests)
}

func SmartShieldMiddleware(shieldID string, route utils.ProxyRouteConfig) func(http.Handler) http.Handler {
	policy := utils.ApplySmartShieldDefaults(route.SmartShield)
	inflight := shield.shieldInflight(shieldID)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := GetClientID(r, route)
			clientID := GetShieldIdentity(r, clientIP)
			// a whitelisted IP is never budgeted, throttled or struck
			privileged := isPrivileged(r, policy) || utils.IsShieldWhitelisted(clientIP)

			wrapper := &SmartResponseWriterWrapper{
				ResponseWriter: w,
				TimeStarted:    time.Now(),
				ClientID:       clientID,
				ClientIP:       clientIP,
				RequestCost:    1,
				Method:         r.Method,
				shieldID:       shieldID,
				policy:         policy,
				isPrivileged:   privileged,
			}

			if !policy.Enabled {
				next.ServeHTTP(wrapper, r)
				finishRequest(wrapper, route, r)
				return
			}

			if !privileged {
				profile := utils.GetSmartShieldProfile()
				current := int(inflight.Load()) + 1
				if current > policy.MaxGlobalSimultaneous*10 {
					rejectTooManyRequests(w, fmt.Sprintf("way too many requests on %s (%d), rejecting right away", shieldID, current))
					return
				}
				if current > policy.MaxGlobalSimultaneous {
					deadline := time.Now().Add(profile.GlobalCapWait)
					for int(inflight.Load())+1 > policy.MaxGlobalSimultaneous {
						if time.Now().After(deadline) {
							rejectTooManyRequests(w, fmt.Sprintf("too many requests on %s (%d of %d)", shieldID, current, policy.MaxGlobalSimultaneous))
							return
						}
						time.Sleep(profile.GlobalCapPoll)
					}
				}
			}

			// the request being judged is part of the usage it is judged on
			budget := shield.client(shieldID, clientID)
			budget.begin(wrapper.TimeStarted)
			budget.addRequests(1, wrapper.TimeStarted)
			userConsumed := budget.consumed(clientID, time.Now())

			if !privileged && !isAllowedToRequest(shieldID, policy, userConsumed) {
				budget.addRequests(-1, wrapper.TimeStarted)
				budget.end(wrapper.TimeStarted, wrapper.TimeStarted)
				lastBan := GetLastBan(clientID)
				go metrics.PushShieldMetrics("smart-shield")
				utils.IncrementIPAbuseCounter(clientIP)

				utils.TriggerEvent(
					"cosmos.proxy.shield.abuse."+route.Name,
					"Proxy Shield "+route.Name+" Abuse by "+clientID,
					"warning",
					"route@"+route.Name,
					map[string]interface{}{
						"route":    route.Name,
						"consumed": userConsumed,
						"lastBan":  lastBan,
						"clientID": clientIP,
						"identity": clientID,
						"hostname": r.Host,
						"url":      r.URL,
					})

				utils.Log("SmartShield: User is blocked due to abuse: " + describeBan(lastBan))
				http.Error(w, "Too many requests", http.StatusTooManyRequests)
				return
			}

			if !privileged {
				wrapper.ThrottleNext = computeThrottle(policy, userConsumed)
			}
			wrapper.budget = budget

			In20Minutes := strconv.FormatInt(time.Now().Add(20*time.Minute).Unix(), 10)
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(calculateLowestExhaustedPercentage(policy, userConsumed), 10))
			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(int64(policy.PerUserRequestLimit), 10))
			w.Header().Set("X-RateLimit-Reset", In20Minutes)

			inflight.Add(1)
			defer func() {
				inflight.Add(-1)
				finishRequest(wrapper, route, r)
				budget.end(wrapper.TimeStarted, wrapper.TimeEnded)
			}()

			next.ServeHTTP(wrapper, r)
		})
	}
}
