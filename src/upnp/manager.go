package upnp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	// Mappings are leased, not permanent: if Cosmos stops, the router closes
	// the ports by itself within the hour.
	leaseSeconds  = 3600
	renewInterval = 20 * time.Minute
)

// Mapping is one port forwarded (or to be forwarded) on the router.
type Mapping struct {
	Name         string `json:"name"`
	Protocol     string `json:"protocol"`
	InternalPort int    `json:"internalPort"`
	ExternalPort int    `json:"externalPort"`
	Active       bool   `json:"active"`
	Error        string `json:"error,omitempty"`
}

// Status is the outcome of the last attempt to bring the router in line with
// the configuration.
type Status struct {
	Enabled     bool      `json:"enabled"`
	Gateway     string    `json:"gateway"`
	GatewayIP   string    `json:"gatewayIP"`
	LocalIP     string    `json:"localIP"`
	ExternalIP  string    `json:"externalIP"`
	Mappings    []Mapping `json:"mappings"`
	Warning     string    `json:"warning,omitempty"`
	Error       string    `json:"error,omitempty"`
	LastRefresh time.Time `json:"lastRefresh"`
}

var (
	runMu   sync.Mutex // one reconciliation at a time
	applied []Mapping  // mappings this process created and still owns

	statusMu sync.Mutex
	status   = Status{Mappings: []Mapping{}}

	renewOnce sync.Once
)

// Apply brings the router's port forwards in line with the configuration. It
// returns at once; the work happens in the background. Call it whenever the
// server (re)starts, which is also when a changed configuration takes effect.
func Apply() {
	go reconcile(true)
	renewOnce.Do(func() {
		go func() {
			for range time.Tick(renewInterval) {
				runMu.Lock()
				idle := len(applied) == 0
				runMu.Unlock()
				if idle && !utils.GetMainConfig().HTTPConfig.UPnP.Enabled {
					continue
				}
				reconcile(false)
			}
		}()
	})
}

// GetStatus returns the outcome of the last reconciliation.
func GetStatus() Status {
	statusMu.Lock()
	defer statusMu.Unlock()
	return status
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}

// desiredMappings lists what the configuration asks the router to forward.
// Only Cosmos' own listening ports can be forwarded, and the plain HTTP port
// only when asked for explicitly.
func desiredMappings(cfg utils.HTTPConfig) ([]Mapping, error) {
	if !cfg.UPnP.Enabled {
		return nil, nil
	}

	build := func(name, internal, external string) (Mapping, error) {
		in, err := parsePort(internal)
		if err != nil {
			return Mapping{}, fmt.Errorf("%s port: %w", name, err)
		}
		out := in
		if strings.TrimSpace(external) != "" {
			if out, err = parsePort(external); err != nil {
				return Mapping{}, fmt.Errorf("external %s port: %w", name, err)
			}
		}
		return Mapping{Name: name, Protocol: "TCP", InternalPort: in, ExternalPort: out}, nil
	}

	wanted := []Mapping{}
	if cfg.HTTPSCertificateMode != utils.HTTPSCertModeList["DISABLED"] {
		m, err := build("HTTPS", cfg.HTTPSPort, cfg.UPnP.ExternalHTTPSPort)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, m)
	}
	if cfg.UPnP.ForwardHTTP {
		m, err := build("HTTP", cfg.HTTPPort, cfg.UPnP.ExternalHTTPPort)
		if err != nil {
			return nil, err
		}
		wanted = append(wanted, m)
	}

	if len(wanted) == 0 {
		return nil, errors.New("HTTPS is disabled, so there is nothing to forward: enable HTTPS, or choose to forward the HTTP port")
	}
	if len(wanted) == 2 && wanted[0].ExternalPort == wanted[1].ExternalPort {
		return nil, errors.New("HTTP and HTTPS cannot use the same external port")
	}
	return wanted, nil
}

func sameForward(a, b Mapping) bool {
	return a.Protocol == b.Protocol && a.ExternalPort == b.ExternalPort
}

// privateAddressNote explains why a forward on a router whose own WAN address
// is not public will not make the server reachable.
func privateAddressNote(externalIP string) string {
	ip := net.ParseIP(externalIP)
	if ip == nil {
		return ""
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) {
		return "The router's own internet address (" + externalIP + ") is not a public one, so it sits behind another router or the provider's NAT. Forwarding ports here will not make Cosmos reachable from the internet."
	}
	return ""
}

// reconcile removes forwards that are no longer wanted and creates or renews
// the wanted ones. verbose logs successes too (periodic renewals stay quiet).
func reconcile(verbose bool) Status {
	runMu.Lock()
	defer runMu.Unlock()

	cfg := utils.GetMainConfig().HTTPConfig
	wanted, cfgErr := desiredMappings(cfg)

	st := Status{Enabled: cfg.UPnP.Enabled, Mappings: []Mapping{}, LastRefresh: time.Now()}
	if cfgErr != nil {
		st.Error = cfgErr.Error()
	}
	publish := func() Status {
		statusMu.Lock()
		status = st
		statusMu.Unlock()
		return st
	}

	if len(wanted) == 0 && len(applied) == 0 {
		return publish() // nothing to create and nothing of ours to remove
	}

	g, err := discover()
	if err != nil {
		st.Error = err.Error()
		st.Mappings = append(st.Mappings, wanted...)
		utils.Error("UPnP: cannot reach the router", err)
		return publish()
	}
	st.Gateway = g.name
	st.GatewayIP = g.ip.String()
	st.LocalIP = g.localIP.String()
	if ip, err := g.externalIP(); err == nil {
		st.ExternalIP = ip
		st.Warning = privateAddressNote(ip)
	}

	for _, old := range applied {
		keep := false
		for _, m := range wanted {
			keep = keep || sameForward(old, m)
		}
		if keep {
			continue
		}
		if err := g.removeMapping(old.Protocol, old.ExternalPort); err != nil {
			utils.Error("UPnP: could not remove the forward of port "+strconv.Itoa(old.ExternalPort), err)
		} else {
			utils.Log("UPnP: removed the forward of port " + strconv.Itoa(old.ExternalPort))
		}
	}

	applied = nil
	for _, m := range wanted {
		err := g.addMapping(m.Protocol, m.ExternalPort, m.InternalPort, "Cosmos "+m.Name, leaseSeconds)
		if err != nil {
			m.Error = err.Error()
			utils.Error("UPnP: could not forward port "+strconv.Itoa(m.ExternalPort), err)
		} else {
			m.Active = true
			applied = append(applied, m)
			if verbose {
				utils.Log(fmt.Sprintf("UPnP: router forwards port %d to %s:%d (%s)", m.ExternalPort, st.LocalIP, m.InternalPort, m.Name))
			}
		}
		st.Mappings = append(st.Mappings, m)
	}

	return publish()
}

// StatusRoute godoc
// @Summary UPnP port forwarding status
// @Description GET returns the outcome of the last attempt to forward Cosmos' ports on the router; POST retries now and returns the new outcome
// @Tags config
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Router /api/upnp [get]
// @Router /api/upnp [post]
func StatusRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_ADMIN) != nil {
		return
	}

	var st Status
	switch req.Method {
	case "GET":
		st = GetStatus()
	case "POST":
		// Takes no input: it only re-applies the saved configuration.
		st = reconcile(true)
	default:
		utils.Error("UPnP: Method not allowed "+req.Method, nil)
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "OK",
		"data":   st,
	})
}
