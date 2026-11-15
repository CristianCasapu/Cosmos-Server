package anyimage

// The one HTTP client that reads registries and Docker Hub: https only,
// through guardedDial, bounded in time, size and redirects, with the token
// authentication registries use for public images (Docker Hub, GHCR, Quay)
// and the server's own credentials where it has them.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Failures, as the codes the panel turns into sentences.
var (
	errNotFound     = errors.New("notFound")
	errUnauthorized = errors.New("unauthorized")
	errRateLimited  = errors.New("rateLimited")
	errTooLarge     = errors.New("tooLarge")
	errUnreachable  = errors.New("unreachable")
	errNotImage     = errors.New("notImage")
	errRedirect     = errors.New("redirect refused")
)

const maxRedirects = 3

// testRoots, when set by the tests, replaces the system's certificate authorities.
var testRoots *x509.CertPool

var (
	httpClientOnce sync.Once
	httpClientIs   *http.Client
)

func httpClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			Proxy:                  nil, // a proxy would bypass the dial check
			DialContext:            guardedDial,
			TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: testRoots},
			TLSHandshakeTimeout:    6 * time.Second,
			ResponseHeaderTimeout:  8 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConns:           8,
			IdleConnTimeout:        30 * time.Second,
			ForceAttemptHTTP2:      true,
		}
		httpClientIs = &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return errRedirect
				}
				if req.URL.Scheme != "https" {
					return errRedirect
				}
				// credentials never follow a redirect to another host (blob storage)
				if req.URL.Host != via[0].URL.Host {
					req.Header.Del("Authorization")
				}
				return nil
			},
		}
	})
	return httpClientIs
}

// resetClient is for the tests, after changing testRoots.
func resetClient() {
	httpClientOnce = sync.Once{}
}

// readBounded reads at most limit bytes; more is errTooLarge.
func readBounded(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, errUnreachable
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

// classify turns a transport error or a status into one of the codes.
func classify(err error, status int) error {
	if err != nil {
		if errors.Is(err, errRefusedAddress) {
			return ErrInternal
		}
		if errors.Is(err, errRedirect) {
			return errRedirect
		}
		return errUnreachable
	}
	switch {
	case status == http.StatusNotFound:
		return errNotFound
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errUnauthorized
	case status == http.StatusTooManyRequests:
		return errRateLimited
	}
	return errUnreachable
}

// registryClient reads one repository of one registry.
type registryClient struct {
	ref   Reference
	token string
	basic string
}

type tokenAnswer struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

// challenge reads a WWW-Authenticate header: the scheme and its parameters.
func challenge(header string) (string, map[string]string) {
	header = strings.TrimSpace(header)
	space := strings.IndexByte(header, ' ')
	if space < 0 {
		return strings.ToLower(header), map[string]string{}
	}
	scheme := strings.ToLower(header[:space])
	params := map[string]string{}
	rest := header[space+1:]
	for len(rest) > 0 && len(params) < 8 {
		rest = strings.TrimLeft(rest, " ,")
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = rest[eq+1:]
		value := ""
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				break
			}
			value = rest[1 : end+1]
			rest = rest[end+2:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			value = strings.TrimSpace(rest[:end])
			rest = rest[end:]
		}
		params[key] = value
	}
	return scheme, params
}

// realmMayGetCredentials: the registry's own host, or Docker Hub's token service for Docker Hub.
func realmMayGetCredentials(ref Reference, realm *url.URL) bool {
	host := strings.ToLower(realm.Host)
	if ref.Registry == hubRegistry {
		return host == "auth.docker.io"
	}
	return host == strings.ToLower(ref.Registry)
}

// authenticate answers a 401 challenge: a token from the realm, or basic credentials.
func (c *registryClient) authenticate(ctx context.Context, header string) error {
	scheme, params := challenge(header)
	user, pass := credentialsFor(c.ref.Registry)
	switch scheme {
	case "basic":
		if user == "" {
			return errUnauthorized
		}
		c.basic = base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		return nil
	case "bearer":
	default:
		return errUnauthorized
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || realm.Scheme != "https" || realm.Host == "" || realm.User != nil {
		return errUnauthorized
	}
	query := realm.Query()
	if params["service"] != "" {
		query.Set("service", params["service"])
	}
	query.Set("scope", "repository:"+c.ref.Repository+":pull")
	realm.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return errUnauthorized
	}
	if user != "" && realmMayGetCredentials(c.ref, realm) {
		req.SetBasicAuth(user, pass)
	}
	res, err := httpClient().Do(req)
	if err != nil {
		return classify(err, 0)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return classify(nil, res.StatusCode)
	}
	data, err := readBounded(res.Body, 64<<10)
	if err != nil {
		return err
	}
	var answer tokenAnswer
	if json.Unmarshal(data, &answer) != nil {
		return errUnauthorized
	}
	c.token = answer.Token
	if c.token == "" {
		c.token = answer.AccessToken
	}
	if c.token == "" || len(c.token) > 16<<10 {
		return errUnauthorized
	}
	return nil
}

// do sends one request to the registry, authenticating once when asked.
// The answer's body is read up to limit.
func (c *registryClient) do(ctx context.Context, method, path string, accept []string, limit int64) (*http.Response, []byte, error) {
	target := "https://" + c.ref.registryHost() + path
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, nil)
		if err != nil {
			return nil, nil, errUnreachable
		}
		if len(accept) > 0 {
			req.Header.Set("Accept", strings.Join(accept, ", "))
		}
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		} else if c.basic != "" {
			req.Header.Set("Authorization", "Basic "+c.basic)
		}
		res, err := httpClient().Do(req)
		if err != nil {
			return nil, nil, classify(err, 0)
		}
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 && c.token == "" && c.basic == "" {
			header := res.Header.Get("WWW-Authenticate")
			res.Body.Close()
			if err := c.authenticate(ctx, header); err != nil {
				return nil, nil, err
			}
			continue
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return res, nil, classify(nil, res.StatusCode)
		}
		if method == http.MethodHead {
			return res, nil, nil
		}
		data, err := readBounded(res.Body, limit)
		return res, data, err
	}
	return nil, nil, errUnauthorized
}

// getJSON reads a JSON answer from any https address through the same client
// (Docker Hub's API), bounded.
func getJSON(ctx context.Context, address string, limit int64, into interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return errUnreachable
	}
	req.Header.Set("Accept", "application/json")
	res, err := httpClient().Do(req)
	if err != nil {
		return classify(err, 0)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return classify(nil, res.StatusCode)
	}
	data, err := readBounded(res.Body, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("%w: not JSON", errUnreachable)
	}
	return nil
}
