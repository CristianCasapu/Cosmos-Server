package main

// Fork addition (plan 46): upstream's own start and reload functions
// (startHTTPServer, startHTTPSServer, reloadForRestart) under the restart
// lifecycle, without InitServer (which needs Docker and the databases): a
// router of the test's own is handed to them.

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/gorilla/mux"
)

func freePortNumber(t *testing.T) string {
	_, port, _ := net.SplitHostPort(freePort(t))
	return port
}

// startForTest runs start with ports of the test's own and the given
// configuration, as StartServer does, until a restart ends the generation.
func startForTest(t *testing.T, config utils.Config, start func(*mux.Router) error) (httpPort, httpsPort string, ended chan error) {
	t.Helper()
	previous := utils.GetBaseMainConfig()
	previousHTTP, previousHTTPS := serverPortHTTP, serverPortHTTPS
	httpPort, httpsPort = freePortNumber(t), freePortNumber(t)
	config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort = httpPort, httpsPort
	utils.LoadBaseMainConfig(config)
	serverPortHTTP, serverPortHTTPS = httpPort, httpsPort
	router := mux.NewRouter()
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "router "+r.Proto) })
	ended = make(chan error, 1)
	go func() { ended <- start(router) }()
	t.Cleanup(func() {
		forkRestarts.release()
		serverPortHTTP, serverPortHTTPS = previousHTTP, previousHTTPS
		utils.LoadBaseMainConfig(previous)
	})
	return httpPort, httpsPort, ended
}

func untilAnswered(t *testing.T, url string, want string) {
	t.Helper()
	c := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // the test's certificate
	var last string
	for i := 0; i < 300; i++ {
		res, err := c.Get(url)
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			last = res.Status + " " + string(body)
			if strings.Contains(last, want) {
				return
			}
		} else {
			last = err.Error()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: want %q, got %q", url, want, last)
}

func TestStartHTTPServerServesUntilARestart(t *testing.T) {
	config := utils.DefaultConfig
	config.HTTPConfig.Hostname = "server.local"
	httpPort, _, ended := startForTest(t, config, startHTTPServer)
	untilAnswered(t, "http://127.0.0.1:"+httpPort+"/", "router HTTP/1.1")
	forkRestarts.restart(func() {})
	select {
	case err := <-ended:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("a restart must end startHTTPServer with ErrServerClosed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startHTTPServer did not end on a restart")
	}
}

func TestStartHTTPSServerServesBothListenersAsOneGeneration(t *testing.T) {
	certPEM, keyPEM := testCertPEM(t, "server.local")
	config := utils.DefaultConfig
	config.HTTPConfig.Hostname = "server.local"
	config.HTTPConfig.HTTPSCertificateMode = utils.HTTPSCertModeList["SELFSIGNED"]
	config.HTTPConfig.TLSCert, config.HTTPConfig.TLSKey = certPEM, keyPEM
	config.HTTPConfig.SelfTLSCert, config.HTTPConfig.SelfTLSKey = certPEM, keyPEM
	httpPort, httpsPort, ended := startForTest(t, config, startHTTPSServer)
	// HTTPS with HTTP/2 (nameHTTP2 before the handoff), and the plain listener
	untilAnswered(t, "https://127.0.0.1:"+httpsPort+"/", "router HTTP/2.0")
	// the plain listener answers (a redirect or the not-found page of a name served elsewhere)
	c := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if res, err := c.Get("http://127.0.0.1:" + httpPort + "/"); err != nil {
		t.Fatalf("the plain-HTTP listener of the generation does not answer: %v", err)
	} else {
		res.Body.Close()
	}
	forkRestarts.restart(func() {})
	select {
	case err := <-ended:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("a restart must end startHTTPSServer with ErrServerClosed, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startHTTPSServer did not end on a restart")
	}
}

// reloadForRestart runs one at a time: nothing it calls may ask for a
// restart synchronously (it would only be recorded, and a callee doing it
// every time would reload for ever).
func TestReloadForRestartNeverRestartsItself(t *testing.T) {
	previousFolder, previous := utils.CONFIGFOLDER, utils.GetBaseMainConfig()
	previousRestart := utils.RestartHTTPServer
	utils.CONFIGFOLDER = t.TempDir() + "/"
	t.Setenv("CONFIG_FILE", "")
	t.Cleanup(func() {
		utils.CONFIGFOLDER = previousFolder
		utils.LoadBaseMainConfig(previous)
		utils.RestartHTTPServer = previousRestart
		select { // the generation this asked for: nobody serves in this test
		case <-forkRestarts.wanted:
		default:
		}
	})
	config := utils.DefaultConfig
	config.HTTPConfig.Hostname = "server.local"
	utils.SetBaseMainConfig(config)
	utils.RestartHTTPServer = RestartHTTPServer // as InitServer sets it

	before := forkRestarts.reentered
	done := make(chan struct{})
	go func() { RestartHTTPServer(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("RestartHTTPServer did not return")
	}
	if forkRestarts.reentered != before {
		t.Error("something reloadForRestart calls asked for a restart synchronously")
	}
}
