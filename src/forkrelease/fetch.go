package forkrelease

// Fork addition: the only way the updater talks to the outside.
//
// Requests go to GitHub over HTTPS and nowhere else: github.com (the release
// files), api.github.com (the list of releases) and names under
// githubusercontent.com (where GitHub redirects a release file to; that name
// has changed before, and what is fetched is checked by signature whatever
// the host). A redirect is followed only to such an address. Every request
// has time limits and every answer a size limit.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxReleaseList bounds the JSON list of releases.
	MaxReleaseList = 8 << 20
	// MaxChecksumFile bounds a .md5 file.
	MaxChecksumFile = 1024
	// MaxZip bounds a release zip.
	MaxZip = 1 << 30

	maxRedirects = 5

	// ListTimeout bounds a small request, ZipTimeout the download of a zip.
	ListTimeout = 45 * time.Second
	ZipTimeout  = 20 * time.Minute
)

var (
	ErrAddress  = errors.New("the address is not one of GitHub's over HTTPS")
	ErrTooLarge = errors.New("the answer is larger than allowed")
)

// githubHost reports whether a host name is one the updater may talk to.
func githubHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || host == "api.github.com" ||
		(strings.HasSuffix(host, ".githubusercontent.com") && len(host) > len(".githubusercontent.com"))
}

// fetcher is an HTTP client under these rules. The zero value is the one the
// server and the launcher use; tests point it at a local server.
type fetcher struct {
	dial      func(ctx context.Context, network string, address string) (net.Conn, error)
	tlsConfig *tls.Config
}

func (f fetcher) allowed(u *url.URL) error {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return ErrAddress
	}
	if port := u.Port(); port != "" && port != "443" {
		return ErrAddress
	}
	if !githubHost(u.Hostname()) {
		return ErrAddress
	}
	return nil
}

func (f fetcher) client(total time.Duration) *http.Client {
	dial := f.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 15 * time.Second}).DialContext
	}
	tlsConfig := f.tlsConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &http.Client{
		Timeout: total,
		Transport: &http.Transport{
			Proxy:                  http.ProxyFromEnvironment,
			DialContext:            dial,
			TLSClientConfig:        tlsConfig,
			TLSHandshakeTimeout:    15 * time.Second,
			ResponseHeaderTimeout:  30 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			DisableKeepAlives:      true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return f.allowed(req.URL)
		},
	}
}

// open sends a GET and returns the answer when it is a 200 within the limit.
func (f fetcher) open(rawURL string, limit int64, total time.Duration) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrAddress
	}
	if err := f.allowed(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrAddress
	}
	req.Header.Set("User-Agent", "cosmos-fork-updater")
	resp, err := f.client(total).Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) && errors.Is(urlErr.Err, ErrAddress) {
			return nil, fmt.Errorf("a redirect led away from GitHub: %w", ErrAddress)
		}
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("answered %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		resp.Body.Close()
		return nil, ErrTooLarge
	}
	return resp, nil
}

func (f fetcher) get(rawURL string, limit int64, total time.Duration) ([]byte, error) {
	resp, err := f.open(rawURL, limit, total)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, ErrTooLarge
	}
	return body, nil
}

// download writes the answer to a new file (0600, never through a link) and
// returns its SHA-256. On any failure the file is removed.
func (f fetcher) download(path string, rawURL string, limit int64, total time.Duration) (sum string, err error) {
	resp, err := f.open(rawURL, limit, total)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	os.Remove(path)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return "", err
	}
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(path)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	if written > limit {
		return "", ErrTooLarge
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return "", errors.New("download failed: the file is incomplete")
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Get fetches a small document from GitHub: at most limit bytes, a 200 only.
func Get(rawURL string, limit int64) ([]byte, error) {
	return fetcher{}.get(rawURL, limit, ListTimeout)
}
