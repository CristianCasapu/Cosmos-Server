package dnsrecords

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// plain-text "what is my IPv4" endpoints, tried in order
var publicIPEndpoints = []string{
	"https://api.ipify.org",
	"https://ipv4.icanhazip.com",
	"https://checkip.amazonaws.com",
}

// forced IPv4: the records managed are A records
var publicIPClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
	},
}

// DetectPublicIP asks the outside world which IPv4 this server shows up as
func DetectPublicIP() (string, error) {
	var lastErr error = errors.New("no endpoint configured")

	for _, endpoint := range publicIPEndpoints {
		resp, err := publicIPClient.Get(endpoint)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			lastErr = errors.New("unexpected answer from " + endpoint)
			continue
		}

		ip := net.ParseIP(strings.TrimSpace(string(body)))
		if ip == nil || ip.To4() == nil {
			lastErr = errors.New("unexpected answer from " + endpoint)
			continue
		}
		return ip.To4().String(), nil
	}

	return "", lastErr
}
