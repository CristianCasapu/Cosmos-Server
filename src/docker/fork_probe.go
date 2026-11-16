package docker

// Fork addition (plan 43): does a container's port speak web (HTTP) at all?
//
// One bounded question, asked by an administrator's action from the panel
// (the New URL and Internet dialogs ask it for the port they would point
// at): Cosmos connects to the container's own address on this server, as
// Docker gives it, sends one plain "GET / HTTP/1.0" and reads at most
// probeRead bytes for at most probeWait. Nothing is followed (no redirect,
// no second request except one TLS handshake when the port answered in
// TLS). Never another host: the address comes from Docker's inspect of a
// running container of this server, never from the request, and a
// container on the host's network is not asked (its "own address" would be
// the server itself). Limited per user.
//
// The answer says what the port is, never what it said beyond its first
// line (an HTTP status line or a service's greeting, cleaned and cut).

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	"github.com/gorilla/mux"
)

const (
	probeWait         = 3 * time.Second
	probeRead         = 512
	probePerMin       = 20
	probeServerPerMin = 60 // for the whole server, every user together
	probeBodySize     = 1 << 10
)

// ProbeResult is the answer of POST /api/servapps/{containerId}/probe.
type ProbeResult struct {
	Port int `json:"port"`
	// Answer: http, https, other, silent, closed, refused, unreachable, notRunning, hostNetwork, missing
	Answer string `json:"answer"`
	// Protocol: what the greeting looks like (postgresql, mysql, redis, mongodb, ssh, smtp, ftp, imap, pop3, amqp), "" when not known
	Protocol string `json:"protocol,omitempty"`
	// First: the first line of the answer, cleaned, at most 120 characters
	First string `json:"first,omitempty"`
}

// probeDial is a variable so that tests dial their own listener.
var probeDial = func(ctx context.Context, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: probeWait}
	return d.DialContext(ctx, "tcp", address)
}

// probeAddress is where the port is asked: the container's own address.
var probeAddress = func(ctx context.Context, name string) (string, string) {
	if err := Connect(); err != nil {
		return "", "unreachable"
	}
	c, err := DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		return "", "missing"
	}
	return probeAddressOf(c)
}

// probeAddressOf is where a container's port may be asked, from its inspect.
func probeAddressOf(c types.ContainerJSON) (string, string) {
	if c.HostConfig != nil && string(c.HostConfig.NetworkMode) == "host" {
		return "", "hostNetwork"
	}
	ip, state := targetOf(c, 0)
	if state != TargetRunning {
		return "", "notRunning"
	}
	// review C1: never the server itself, whatever the mode (a container in
	// "container:" mode of one on the host's network answers 127.0.0.1)
	if parsed := net.ParseIP(ip); parsed == nil || parsed.IsLoopback() || parsed.IsUnspecified() {
		return "", "hostNetwork"
	}
	return ip, ""
}

// greeting names a service by what it says first or answers to a GET.
func greeting(data []byte) (answer string, protocol string) {
	text := string(data)
	switch {
	case len(data) == 0:
		return "closed", ""
	case strings.HasPrefix(text, "HTTP/"):
		// MongoDB answers a GET on its own port with a page saying it is not web
		if strings.Contains(text, "access MongoDB over HTTP") {
			return "other", "mongodb"
		}
		return "http", ""
	case data[0] == 0x15 || data[0] == 0x16:
		return "tls", ""
	case strings.HasPrefix(text, "SSH-"):
		return "other", "ssh"
	case strings.HasPrefix(text, "* OK"):
		return "other", "imap"
	case strings.HasPrefix(text, "+OK"):
		return "other", "pop3"
	case strings.HasPrefix(text, "-ERR") || strings.HasPrefix(text, "-NOAUTH") || strings.HasPrefix(text, "-DENIED"):
		return "other", "redis"
	case strings.HasPrefix(text, "AMQP"):
		return "other", "amqp"
	case strings.HasPrefix(text, "220"):
		if strings.Contains(strings.ToUpper(text), "FTP") {
			return "other", "ftp"
		}
		return "other", "smtp"
	case len(data) >= 5 && data[0] == 'E' && bytes.Contains(data, []byte("SFATAL")) || bytes.Contains(data, []byte("startup packet")):
		return "other", "postgresql"
	case len(data) >= 5 && data[4] == 0x0a:
		// a MySQL/MariaDB handshake: length (3 bytes), sequence, protocol 10
		return "other", "mysql"
	}
	return "other", ""
}

func firstLine(data []byte) string {
	line := string(data)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	line = cleanLogLine(line)
	out := []rune{}
	for _, r := range line {
		if len(out) >= 120 {
			break
		}
		if r >= 0x20 && r < 0x7f {
			out = append(out, r)
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

// askPort asks one address: a GET, at most probeRead bytes back.
func askPort(ctx context.Context, address string, hostName string) ProbeResult {
	ctx, cancel := context.WithTimeout(ctx, probeWait)
	defer cancel()
	conn, err := probeDial(ctx, address)
	if err != nil {
		if strings.Contains(err.Error(), "refused") {
			return ProbeResult{Answer: "refused"}
		}
		return ProbeResult{Answer: "unreachable"}
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	request := "GET / HTTP/1.0\r\nHost: " + hostName + "\r\nUser-Agent: Cosmos-port-check\r\nAccept: */*\r\nConnection: close\r\n\r\n"
	conn.Write([]byte(request))
	data, err := io.ReadAll(io.LimitReader(conn, probeRead))
	if len(data) == 0 && err != nil {
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			return ProbeResult{Answer: "silent"}
		}
	}
	answer, protocol := greeting(data)
	if answer == "tls" {
		return askTLS(ctx, address, hostName)
	}
	return ProbeResult{Answer: answer, Protocol: protocol, First: firstLine(data)}
}

// askTLS is the same question in TLS, once: an app's own certificate is
// its own, there is nothing to check it against.
func askTLS(ctx context.Context, address string, hostName string) ProbeResult {
	raw, err := probeDial(ctx, address)
	if err != nil {
		return ProbeResult{Answer: "other", Protocol: "tls"}
	}
	defer raw.Close()
	deadline, _ := ctx.Deadline()
	raw.SetDeadline(deadline)
	conn := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: hostName})
	if conn.HandshakeContext(ctx) != nil {
		return ProbeResult{Answer: "other", Protocol: "tls"}
	}
	conn.Write([]byte("GET / HTTP/1.0\r\nHost: " + hostName + "\r\nUser-Agent: Cosmos-port-check\r\nConnection: close\r\n\r\n"))
	data, _ := io.ReadAll(io.LimitReader(conn, probeRead))
	if bytes.HasPrefix(data, []byte("HTTP/")) {
		return ProbeResult{Answer: "https", First: firstLine(data)}
	}
	return ProbeResult{Answer: "other", Protocol: "tls", First: firstLine(data)}
}

// ProbeContainerPort asks a container's port.
func ProbeContainerPort(ctx context.Context, name string, port int) ProbeResult {
	ip, why := probeAddress(ctx, name)
	if why != "" {
		return ProbeResult{Port: port, Answer: why}
	}
	result := askPort(ctx, net.JoinHostPort(ip, strconv.Itoa(port)), name)
	result.Port = port
	return result
}

var (
	probeMu    sync.Mutex
	probeUsers = map[string][]time.Time{}
)

// probeAllowed bounds the questions per user and minute.
func probeAllowed(user string, now time.Time) bool {
	probeMu.Lock()
	defer probeMu.Unlock()
	if len(probeUsers) > 256 {
		probeUsers = map[string][]time.Time{}
	}
	recent := []time.Time{}
	for _, at := range probeUsers[user] {
		if now.Sub(at) < time.Minute {
			recent = append(recent, at)
		}
	}
	limit := probePerMin
	if user == "\x00server" {
		limit = probeServerPerMin
	}
	if len(recent) >= limit {
		probeUsers[user] = recent
		return false
	}
	probeUsers[user] = append(recent, now)
	return true
}

// ProbeRoute answers POST /cosmos/api/servapps/{containerId}/probe {"port": 5432}.
func ProbeRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != http.MethodPost {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	name := mux.Vars(req)["containerId"]
	if !containerNamePattern.MatchString(name) {
		utils.HTTPError(w, "Invalid container name", http.StatusBadRequest, "DS020")
		return
	}
	var body struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(io.LimitReader(req.Body, probeBodySize)).Decode(&body); err != nil || body.Port < 1 || body.Port > 65535 {
		utils.HTTPError(w, "Invalid port", http.StatusBadRequest, "DS021")
		return
	}
	if !probeAllowed(probeUser(req), time.Now()) || !probeAllowed("\x00server", time.Now()) {
		utils.HTTPError(w, "Too many port checks: wait a minute", http.StatusTooManyRequests, "DS022")
		return
	}
	result := ProbeContainerPort(req.Context(), name, body.Port)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": result})
}

// StartReportRoute answers GET /cosmos/api/servapps/{containerId}/start-report:
// the state, the end of the log (masked) and what it says is missing. The
// logs' own permission: the log may hold what the image printed.
func StartReportRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES_READ) != nil {
		return
	}
	if req.Method != http.MethodGet {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	name := mux.Vars(req)["containerId"]
	if !containerNamePattern.MatchString(name) {
		utils.HTTPError(w, "Invalid container name", http.StatusBadRequest, "DS020")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	report, err := StartReportOf(ctx, name, nil)
	if err != nil && report.State != "missing" {
		utils.HTTPError(w, "Docker could not be asked", http.StatusInternalServerError, "DS023")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": report})
}

// containerNamePattern is Docker's own rule for names.
var containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func probeUser(req *http.Request) string {
	ctx := utils.GetAuthContext(req)
	if ctx.APIToken != nil {
		return "token:" + ctx.APIToken.Name
	}
	return "user:" + ctx.Nickname
}
