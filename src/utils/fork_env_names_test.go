package utils

import (
	"os"
	"testing"
)

// Fork addition: tests of fork_env_names.go.

func TestDNSProviderSettingsNeverSetAVariableThatChangesHowTheServerRuns(t *testing.T) {
	refused := []string{
		"LD_PRELOAD", "LD_LIBRARY_PATH", "ld_preload", "PATH", "Path", "HOME", "SHELL", "IFS", "ENV", "BASH_ENV",
		"USER", "TMPDIR", "TZ", "LANG", "LANGUAGE", "LC_ALL", "HOSTNAME", "CONFIG_FILE", "HTTP_PROXY", "HTTPS_PROXY",
		"https_proxy", "ALL_PROXY", "NO_PROXY", "DYLD_INSERT_LIBRARIES", "GCONV_PATH", "DOCKER_HOST", "COSMOS_CONFIG_FOLDER",
		"GODEBUG", "GOMAXPROCS", "GOTRACEBACK", "GOROOT", "SSL_CERT_FILE", "SSL_CERT_DIR", "OPENSSL_CONF", "NODE_OPTIONS",
		"PYTHONPATH", "PERL5OPT", "RUBYOPT", "GIT_SSH_COMMAND", "MALLOC_CHECK_", "LOCPATH", "NLSPATH", "RES_OPTIONS",
		"HOSTALIASES", "EXEC_PATH", "CURL_CA_BUNDLE", "NOTIFY_SOCKET", "ACME_STAGING",
		"", "1ABC", "_X", "A-B", "A B", "A=B", "A\nB", "CF_DNS_API_TOKEN\x00", "ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMN",
	}
	for _, name := range refused {
		if SafeEnvName(name) {
			t.Errorf("%q may be set", name)
		}
	}
	for _, name := range []string{"CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN", "GODADDY_API_KEY", "GCE_PROJECT", "OVH_ENDPOINT", "TF_VAR_fingerprint", "DUCKDNS_TOKEN"} {
		if !SafeEnvName(name) {
			t.Errorf("the provider variable %q is refused", name)
		}
	}

	os.Unsetenv("LD_PRELOAD")
	if SetDNSChallengeEnv("LD_PRELOAD", "/tmp/x.so") {
		t.Error("LD_PRELOAD was set")
	}
	if _, set := os.LookupEnv("LD_PRELOAD"); set {
		t.Error("LD_PRELOAD is in the environment")
	}
	t.Setenv("FORKTEST_DNS_TOKEN", "")
	if !SetDNSChallengeEnv("FORKTEST_DNS_TOKEN", "value") || os.Getenv("FORKTEST_DNS_TOKEN") != "value" {
		t.Error("a provider-like variable was not set")
	}
}
