package proxy

import (
	"fmt"
	"net"
	"sync"
	"time"
	"strings"


	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/azukaar/cosmos-server/src/metrics"
	"github.com/azukaar/cosmos-server/src/constellation"
)

type UDPSmartShieldState struct {
	sync.Mutex
	Connections map[string]*UDPConnectionInfo
}

type UDPConnectionInfo struct {
	ClientID     string
	ShieldID     string
	BytesSent    int64
	LastActivity time.Time
	RouteName		string
}

var udpShield UDPSmartShieldState

func InitUDPShield() {
	udpShield = UDPSmartShieldState{
		Connections: make(map[string]*UDPConnectionInfo),
	}
	go cleanupUDPConnections()
}

func cleanupUDPConnections() {
	go func() {
		for {
			time.Sleep(60 * time.Minute)
			udpShield.Lock()
			// clear all
			udpShield.Connections = make(map[string]*UDPConnectionInfo)
			udpShield.Unlock()
		}
	}()
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			udpShield.Lock()
			for _, info := range udpShield.Connections {
				if info.LastActivity.Add(1 * time.Minute).Before(time.Now()) {
					metrics.PushSetMetric("proxy.route.success."+info.RouteName, int(info.BytesSent), metrics.DataDef{
						Period:    time.Second * 60,
						Label:     "UDP Shield " + info.RouteName,
						AggloType: "sum",
						SetOperation: "max",
						Unit:      "B",
						Decumulate:   true,
					})
				}
			}
			udpShield.Unlock()
		}
	}()
}

func UDPSmartShieldMiddleware(shieldID string, route utils.ProxyRouteConfig) func([]byte, net.Addr) []byte {
	policy := route.SmartShield
	if policy.Enabled {
		profile := utils.GetSmartShieldProfile()
		if policy.PerUserByteLimit == 0 {
			policy.PerUserByteLimit = profile.UDPByteLimit
		}
		if policy.PolicyStrictness == 0 {
			policy.PolicyStrictness = profile.Defaults.PolicyStrictness
		}
	}

	return func(buffer []byte, remoteAddr net.Addr) []byte {
		clientID, _, _ := net.SplitHostPort(remoteAddr.String())
		shieldEntry, shieldWhitelisted := utils.ShieldWhitelistMatch(clientID)

		if !shieldWhitelisted && utils.GetIPAbuseCounter(clientID) > 275 {
			return nil
		}

		if !(clientID == "192.168.1.1" || clientID == "192.168.0.1" || clientID == "192.168.0.254" || clientID == "172.17.0.1") {
			// Whitelist / Constellation check
			if !(shieldWhitelisted && shieldEntry.BypassIPRestriction) && !isAllowedIP(clientID, route) {
				return nil
			}

			// Geo check
			if !(shieldWhitelisted && shieldEntry.BypassGeo) && !isAllowedCountry(clientID, route) {
				return nil
			}

			if policy.Enabled && !shieldWhitelisted && !globalShieldState.allowed(clientID, shieldID, time.Now()) {
				return nil
			}

			udpShield.Lock()
			defer udpShield.Unlock()

			key := fmt.Sprintf("%s-%s", shieldID, clientID)
			info, exists := udpShield.Connections[key]
			if !exists {
				info = &UDPConnectionInfo{
					ClientID: clientID,
					ShieldID: shieldID,
					RouteName: route.Name,
				}
				udpShield.Connections[key] = info
			}

			info.BytesSent += int64(len(buffer))
			info.LastActivity = time.Now()

			if policy.Enabled && !shieldWhitelisted && info.BytesSent > policy.PerUserByteLimit*int64(policy.PolicyStrictness) {
				utils.TriggerEvent(
					"cosmos.proxy.shield.abuse." + route.Name,
					"Socket Shield " + route.Name + " Abuse by " + info.ClientID,
					"warning",
					"route@" + route.Name,
					map[string]interface{}{
					"route": route.Name,
					"consumed": info.BytesSent,
					"lastBan": GetLastBan(info.ClientID),
					"clientID": info.ClientID,
				})

				globalShieldState.strike(info.ClientID, info.ShieldID, utils.ShieldBanReason{
					Limit:   "bytes",
					Used:    float64(info.BytesSent),
					Allowed: float64(policy.PerUserByteLimit * int64(policy.PolicyStrictness)),
					Route:   route.Name,
				}, time.Now())
				return nil
			}

			utils.Debug(fmt.Sprintf("UDPSmartShield: Client %s sent %d bytes, total: %d", clientID, len(buffer), info.BytesSent))
		}
	
		return buffer
	}
}

func isAllowedIP(clientID string, route utils.ProxyRouteConfig) bool {
	whitelistInboundIPs := route.WhitelistInboundIPs
	restrictToConstellation := route.RestrictToConstellation

	isUsingWhitelist := len(whitelistInboundIPs) > 0
	isInWhitelist := false
	isInConstellation := constellation.IsConstellationIP(clientID)
	// see the matching comment in TCPSmartShieldMiddleware
	isLocalPeer := utils.IsLocalPeer(clientID)

	for _, ipRange := range whitelistInboundIPs {
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
				"UDP Socket Shield IP blocked by whitelist",
				"warning",
				"",
				map[string]interface{}{
					"clientID": clientID,
				},
			)
			utils.IncrementIPAbuseCounter(clientID)
			utils.Error(fmt.Sprintf("UDP connection from %s is blocked because of restrictions", clientID), nil)
			return false
		}
	} else if isUsingWhitelist && !isInWhitelist {
		utils.PushShieldMetrics("ip-whitelists")
		utils.TriggerEvent(
			"cosmos.proxy.shield.whitelist",
			"UDP Socket Shield IP blocked by whitelist",
			"warning",
			"",
			map[string]interface{}{
				"clientID": clientID,
			},
		)
		utils.IncrementIPAbuseCounter(clientID)
		utils.Error(fmt.Sprintf("UDP connection from %s is blocked because of restrictions", clientID), nil)
		return false
	}

	return true
}

func isAllowedCountry(clientID string, route utils.ProxyRouteConfig) bool {
	countryCode, err := utils.GetIPLocation(clientID)
	if err != nil {
		utils.Debug("Missing geolocation information to block UDP IPs")
		return true
	}

	config := utils.GetMainConfig()
	countryBlacklistIsWhitelist := config.CountryBlacklistIsWhitelist
	blockedCountries := config.BlockedCountries

	if countryBlacklistIsWhitelist {
		if countryCode != "" {
			blocked := true
			for _, blockedCountry := range blockedCountries {
				if config.ServerCountry != countryCode && countryCode == blockedCountry {
					blocked = false
					break
				}
			}

			if blocked {
				utils.PushShieldMetrics("geo")
				utils.IncrementIPAbuseCounter(clientID)
				utils.TriggerEvent(
					"cosmos.proxy.shield.geo",
					"UDP Proxy Shield Geo blocked",
					"warning",
					"",
					map[string]interface{}{
						"clientID": clientID,
						"country":  countryCode,
						"route":    route.Name,
					},
				)
				utils.Warn(fmt.Sprintf("UDP connection from %s is blocked because of geo restrictions", clientID))
				return false
			}
		}
	} else {
		for _, blockedCountry := range blockedCountries {
			if config.ServerCountry != countryCode && countryCode == blockedCountry {
				utils.PushShieldMetrics("geo")
				utils.IncrementIPAbuseCounter(clientID)
				utils.TriggerEvent(
					"cosmos.proxy.shield.geo",
					"UDP Proxy Shield Geo blocked",
					"warning",
					"",
					map[string]interface{}{
						"clientID": clientID,
						"country":  countryCode,
						"route":    route.Name,
					},
				)
				utils.Warn(fmt.Sprintf("UDP connection from %s is blocked because of geo restrictions", clientID))
				return false
			}
		}
	}

	return true
}