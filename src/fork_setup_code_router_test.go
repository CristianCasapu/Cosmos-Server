package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
)

// The setup code through the server's own router (InitServer), not the
// middleware alone: the setup routes refuse a request without the code in
// every spelling, and the wizard's default path (the clean-install step -1,
// ticked by default, then step 3) goes through with the SAME code, which the
// clean install keeps.
func TestTheSetupCodeThroughTheRealRouter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the router")
	}
	dir := t.TempDir()
	prevFolder := utils.CONFIGFOLDER
	utils.CONFIGFOLDER = filepath.Join(dir, "config") + "/"
	os.MkdirAll(utils.CONFIGFOLDER, 0o700)
	prevConfig := utils.GetBaseMainConfig()
	t.Cleanup(func() {
		utils.CloseStore()
		utils.CONFIGFOLDER = prevFolder
		utils.LoadBaseMainConfig(prevConfig)
	})
	// the router serves ./static: a working folder that has one
	work := filepath.Join(dir, "work")
	os.MkdirAll(filepath.Join(work, "static"), 0o755)
	os.WriteFile(filepath.Join(work, "static", "index.html"), []byte("<html></html>"), 0o644)
	here, _ := os.Getwd()
	os.Chdir(work)
	t.Cleanup(func() { os.Chdir(here) })

	// a fresh installation's configuration, as upstream writes it at the first start
	config := utils.DefaultConfig
	config.NewInstall = true
	utils.LoadBaseMainConfig(config)
	if err := utils.InitStore(); err != nil {
		t.Fatal(err)
	}
	setupCodeOut = &bytes.Buffer{}
	t.Cleanup(func() { setupCodeOut = os.Stderr })
	forkSetupCodeAtStart()
	code, err := readSetupCode(setupCodePath())
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(InitServer())
	defer server.Close()
	post := func(path, code, body string) (int, string) {
		req, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if code != "" {
			req.Header.Set(setupCodeHeader, code)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var answer struct{ Code string }
		json.NewDecoder(resp.Body).Decode(&answer)
		return resp.StatusCode, answer.Code
	}

	for _, path := range []string{"/cosmos/api/newInstall", "/cosmos/api/setup", "/cosmos/api/setup-join", "/cosmos/api/dns",
		"/cosmos/api//newInstall", "/cosmos/api/./setup", "/cosmos/api/x/../setup-join"} {
		status, answer := post(path, "", `{"step":"4","nickname":"intruder","password":"Passw0rd-x!yz"}`)
		if answer == "" && status < 300 {
			t.Errorf("%s answered %d without the code", path, status)
		}
		if status == 200 {
			t.Errorf("%s reached its handler without the code", path)
		}
	}
	if status, answer := post("/cosmos/api/newInstall", "AAAA-AAAA-AAAA-AAAA-AAAA", `{"step":"2"}`); status != 403 || answer != "SC002" {
		t.Errorf("a wrong code: %d %s", status, answer)
	}

	// the wizard's default path: step -1 (clean install), then step 3, same code
	if status, answer := post("/cosmos/api/newInstall", code, `{"step":"-1"}`); status != 200 {
		t.Fatalf("step -1: %d %s", status, answer)
	}
	if _, err := readSetupCode(setupCodePath()); err != nil {
		t.Fatalf("the clean install removed the setup code: %v", err)
	}
	if status, answer := post("/cosmos/api/newInstall", code, `{"step":"3","httpsCertificateMode":"DISABLED","hostname":"server.local"}`); status != 200 {
		t.Fatalf("step 3 after the clean install, with the same code: %d %s", status, answer)
	}
	if status, answer := post("/cosmos/api/newInstall", code, `{"step":"4","nickname":"admin","password":"Passw0rd-x!yz","email":"admin@example.org"}`); status != 200 {
		t.Fatalf("step 4: %d %s", status, answer)
	}
	// the administrator's store is private from its first write
	for _, name := range []string{"auth.db", "auth.db-wal"} {
		if info, err := os.Stat(utils.CONFIGFOLDER + name); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %o", name, info.Mode().Perm())
		}
	}
	_ = rand.Reader
}
