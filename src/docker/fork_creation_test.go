package docker

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	conttype "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

func decodeService(t *testing.T, text string) ContainerCreateRequestContainer {
	t.Helper()
	var service ContainerCreateRequestContainer
	if err := json.Unmarshal([]byte(text), &service); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return service
}

func TestCommandTakesATextOrAnExecFormList(t *testing.T) {
	cases := map[string][]string{
		`{"command": "sleep 3600"}`:                          {"sleep", "3600"},
		`{"command": ["sh", "-c", "echo a b"]}`:              {"sh", "-c", "echo a b"},
		`{"command": "sh -c 'echo a b'"}`:                     {"sh", "-c", "echo a b"},
		`{"command": "--name \"My Server\" --port=8080"}`:    {"--name", "My Server", "--port=8080"},
		`{"command": ["--threads", 4, true]}`:                 {"--threads", "4", "true"},
		`{"command": "a\\ b c"}`:                             {"a b", "c"},
	}
	for text, want := range cases {
		service := decodeService(t, text)
		if got := service.Command.Args(); !reflect.DeepEqual(got, want) || !service.Command.Given() {
			t.Errorf("%s: %q", text, got)
		}
	}
	if service := decodeService(t, `{"entrypoint": ["/init"]}`); !reflect.DeepEqual(service.Entrypoint.Args(), []string{"/init"}) {
		t.Errorf("entrypoint: %q", service.Entrypoint.Args())
	}
	if service := decodeService(t, `{"image": "x"}`); service.Command.Given() {
		t.Error("no command, but one is given")
	}
	var service ContainerCreateRequestContainer
	if err := json.Unmarshal([]byte(`{"command": "echo 'open"}`), &service); err == nil {
		t.Error("an unclosed quote was accepted")
	}
	// the edit form sends back what the export wrote: a list stays a list
	out, _ := json.Marshal(ContainerCreateRequestContainer{Command: forkList([]string{"echo", "a b"})})
	if again := decodeService(t, string(out)); !reflect.DeepEqual(again.Command.Args(), []string{"echo", "a b"}) {
		t.Errorf("round trip: %s -> %q", out, again.Command.Args())
	}
}

func TestHealthcheckTakesComposeDurationsAndTests(t *testing.T) {
	service := decodeService(t, `{"healthcheck": {"test": "curl -f http://localhost/", "interval": "1m30s", "timeout": "10s", "start_period": "500ms", "start_interval": "2s", "retries": 3}}`)
	health := healthConfig(service.HealthCheck)
	if health == nil || !reflect.DeepEqual(health.Test, []string{"CMD-SHELL", "curl -f http://localhost/"}) {
		t.Fatalf("test: %+v", health)
	}
	if health.Interval != 90*time.Second || health.Timeout != 10*time.Second || health.StartPeriod != 500*time.Millisecond || health.StartInterval != 2*time.Second || health.Retries != 3 {
		t.Errorf("durations: %+v", health)
	}
	// what upstream's form sent: whole seconds
	service = decodeService(t, `{"healthcheck": {"test": ["CMD", "true"], "interval": 30, "timeout": 5}}`)
	if health := healthConfig(service.HealthCheck); health.Interval != 30*time.Second || health.Timeout != 5*time.Second {
		t.Errorf("seconds: %+v", health)
	}
	service = decodeService(t, `{"healthcheck": {"disable": true}}`)
	if health := healthConfig(service.HealthCheck); health == nil || !reflect.DeepEqual(health.Test, []string{"NONE"}) {
		t.Errorf("disable: %+v", health)
	}
	if health := healthConfig(decodeService(t, `{}`).HealthCheck); health != nil {
		t.Errorf("none given: %+v", health)
	}
	if service := decodeService(t, `{"healthcheck": {"interval": "1d"}}`); service.HealthCheck.Interval.duration() != 24*time.Hour {
		t.Errorf("days: %v", service.HealthCheck.Interval.duration())
	}
	var bad ContainerCreateRequestContainer
	if err := json.Unmarshal([]byte(`{"healthcheck": {"interval": "soon"}}`), &bad); err == nil {
		t.Error("a duration that is none was accepted")
	}
}

func TestServiceExtrasReachDocker(t *testing.T) {
	service := decodeService(t, `{"shm_size": "256m", "tmpfs": ["/run", "/tmp:size=64m"], "init": true, "read_only": true}`)
	hostConfig := &conttype.HostConfig{}
	if err := applyServiceExtras(service, hostConfig); err != nil {
		t.Fatal(err)
	}
	if hostConfig.ShmSize != 256*1024*1024 || hostConfig.Tmpfs["/run"] != "" || hostConfig.Tmpfs["/tmp"] != "size=64m" || hostConfig.Init == nil || !*hostConfig.Init || !hostConfig.ReadonlyRootfs {
		t.Errorf("host config: %+v", hostConfig)
	}
	if service := decodeService(t, `{"shm_size": 1073741824, "tmpfs": "/cache"}`); service.ShmSize != "1073741824" || !reflect.DeepEqual(service.Tmpfs.Args(), []string{"/cache"}) {
		t.Errorf("number and text: %+v", service.forkServiceExtras)
	}
	// and an edit keeps them: the export reads them back
	extras := exportServiceExtras(hostConfig)
	again := &conttype.HostConfig{}
	if err := applyServiceExtras(ContainerCreateRequestContainer{forkServiceExtras: extras}, again); err != nil || again.ShmSize != hostConfig.ShmSize || !reflect.DeepEqual(again.Tmpfs, hostConfig.Tmpfs) || !*again.Init || !again.ReadonlyRootfs {
		t.Errorf("export: %+v -> %+v (%v)", extras, again, err)
	}
	if extras := exportServiceExtras(&conttype.HostConfig{ShmSize: 64 * 1024 * 1024}); extras.ShmSize != "" {
		t.Errorf("Docker's default size exported: %q", extras.ShmSize)
	}
}

func TestRestartPolicyTakesOnFailureWithACount(t *testing.T) {
	if policy := restartPolicy("on-failure:5"); policy.Name != "on-failure" || policy.MaximumRetryCount != 5 {
		t.Errorf("%+v", policy)
	}
	if policy := restartPolicy("unless-stopped"); policy.Name != "unless-stopped" || policy.MaximumRetryCount != 0 {
		t.Errorf("%+v", policy)
	}
}

func TestShellWordsNeverRunsAnything(t *testing.T) {
	words, err := shellWords("echo $(rm -rf /) `id` ; ls | wc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"echo", "$(rm", "-rf", "/)", "`id`", ";", "ls", "|", "wc"}
	if !reflect.DeepEqual(words, want) {
		t.Errorf("%q", words)
	}
}

func withServerCPUs(t *testing.T, n int) {
	previous := serverCPUs
	serverCPUs = func() int { return n }
	t.Cleanup(func() { serverCPUs = previous })
}

func TestTheFieldsOfTheFormAreCheckedBeforeAnythingIsCreated(t *testing.T) {
	withServerCPUs(t, 4)
	good := []ContainerCreateRequestContainer{
		{User: "1000:1000"}, {User: "abc"}, {User: "www-data:www-data"}, {User: "0"},
		{MemLimit: "512m"}, {MemLimit: "2g"}, {MemLimit: "536870912"},
		{CPUs: 0.5}, {CPUs: 4},
		{NetworkMode: "bridge"}, {NetworkMode: "host", Ports: []string{"80:80"}}, {NetworkMode: "container:gluetun"}, {NetworkMode: "cosmos-app-default"},
		{RestartPolicy: "on-failure:3"}, {RestartPolicy: "unless-stopped"},
	}
	for _, service := range good {
		if err := checkServiceFields(service); err != nil {
			t.Errorf("%+v refused: %v", service, err)
		}
	}
	bad := map[string]ContainerCreateRequestContainer{
		"user":              {User: "a b"},
		"user:":             {User: "1000:"},
		"memory limit":      {MemLimit: "lots"},
		"6m":                {MemLimit: "1m"},
		"CPUs -":            {CPUs: -1},
		"this server has 4": {CPUs: 8},
		"network mode":      {NetworkMode: "bad mode"},
		"cannot publish":    {NetworkMode: "container:gluetun", Ports: []string{"8080:8080"}},
		"restart \"sometimes":  {RestartPolicy: "sometimes"},
		"only on-failure":   {RestartPolicy: "always:3"},
		"shm_size":          {forkServiceExtras: forkServiceExtras{ShmSize: "huge"}},
		"tmpfs":             {forkServiceExtras: forkServiceExtras{Tmpfs: forkList([]string{"run"})}},
	}
	for want, service := range bad {
		err := checkServiceFields(service)
		if err == nil || !strings.Contains(err.Error(), strings.TrimSuffix(want, ":")) {
			t.Errorf("%s: %v", want, err)
		}
	}
	// the label wins over network_mode, as in the create loop
	if err := checkServiceFields(ContainerCreateRequestContainer{Labels: map[string]string{"cosmos-force-network-mode": "container:vpn"}, Ports: []string{"1:1"}}); err == nil {
		t.Error("ports with a forced container network passed")
	}
}

type sensitiveCases struct {
	Extra []string          `json:"extra"`
	Binds map[string]string `json:"binds"`
}

func readSensitiveCases(t *testing.T) sensitiveCases {
	t.Helper()
	content, err := os.ReadFile("testdata/sensitive_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases sensitiveCases
	if err := json.Unmarshal(content, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestSensitiveBindsAreTheOnesOfTheSharedCases(t *testing.T) {
	cases := readSensitiveCases(t)
	for source, want := range cases.Binds {
		if got := SensitiveBind(source, cases.Extra); got != want {
			t.Errorf("%s: %q, want %q", source, got, want)
		}
	}
}

func withSensitiveFakes(t *testing.T, had map[string]bool) {
	t.Helper()
	previousExtra, previousExisting := extraSensitiveDirs, existingSensitive
	extraSensitiveDirs = func() []string { return []string{"/var/lib/cosmos-config"} }
	existingSensitive = func(service string, name string, extra []string) map[string]bool { return had }
	t.Cleanup(func() { extraSensitiveDirs, existingSensitive = previousExtra, previousExisting })
}

func TestSensitiveAccessNeedsAConfirmationPerItem(t *testing.T) {
	withSensitiveFakes(t, map[string]bool{})
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"portainer": {
			Volumes: []mount.Mount{
				{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
				{Type: mount.TypeBind, Source: "/cosmos-storage/portainer", Target: "/data"},
				{Type: mount.TypeVolume, Source: "etc", Target: "/etc/x"},
			},
			Privileged:  true,
			CapAdd:      []string{"CAP_SYS_ADMIN", "CHOWN"},
			NetworkMode: "host",
			SecurityOpt: []string{"apparmor=unconfined", "no-new-privileges:true"},
		},
	}}
	items := SensitiveAccess(request, nil)
	keys := []string{}
	for _, item := range items {
		keys = append(keys, item.Key())
	}
	want := []string{"portainer|socket|/var/run/docker.sock", "portainer|privileged|true", "portainer|network-host|host", "portainer|cap|SYS_ADMIN", "portainer|unconfined|apparmor=unconfined"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("%q", keys)
	}

	query := url.Values{"confirm": want[:4]}
	missing := unconfirmedSensitive(request, query)
	if len(missing) != 1 || missing[0].Kind != "unconfined" {
		t.Errorf("one left unconfirmed: %+v", missing)
	}
	query["confirm"] = want
	if missing := unconfirmedSensitive(request, query); len(missing) != 0 {
		t.Errorf("all confirmed: %+v", missing)
	}

	recorder := httptest.NewRecorder()
	if forkRefuseService(recorder, httptest.NewRequest("POST", "/cosmos/api/docker-service", nil), request, func() {}) {
		t.Fatal("created without a confirmation")
	}
	if body := recorder.Body.String(); !strings.HasPrefix(body, "[OPERATION FAILED]") || !strings.Contains(body, "/var/run/docker.sock") || !strings.Contains(body, "privileged") {
		t.Errorf("answer: %s", body)
	}
}

// Fork addition (plan 40): a device of the server is sensitive access too.
func TestADeviceNeedsAConfirmation(t *testing.T) {
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"jellyfin": {Devices: []string{"/dev/dri:/dev/dri", "/dev/sda:/dev/xvda:rwm"}},
	}}
	keys := []string{}
	for _, item := range SensitiveAccess(request, nil) {
		keys = append(keys, item.Key())
	}
	if !reflect.DeepEqual(keys, []string{"jellyfin|device|/dev/dri", "jellyfin|device|/dev/sda"}) {
		t.Fatalf("%q", keys)
	}
	if missing := unconfirmedSensitive(request, url.Values{"confirm": keys[:1]}); len(missing) != 1 || missing[0].Value != "/dev/sda" {
		t.Fatalf("%+v", missing)
	}
}

func TestAnEditIsNotAskedAgainForWhatTheContainerHad(t *testing.T) {
	withSensitiveFakes(t, map[string]bool{"cosmos|socket|/var/run/docker.sock": true})
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"cosmos": {Volumes: []mount.Mount{
			{Type: mount.TypeBind, Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"},
			{Type: mount.TypeBind, Source: "/etc", Target: "/host-etc"},
		}},
	}}
	missing := unconfirmedSensitive(request, url.Values{"replace": {"1"}})
	if len(missing) != 1 || missing[0].Value != "/etc" {
		t.Errorf("edit: %+v", missing)
	}
	if missing := unconfirmedSensitive(request, url.Values{}); len(missing) != 2 {
		t.Errorf("a creation is asked for both: %+v", missing)
	}
}

func withPlatforms(t *testing.T, server ServerPlatform, images map[string][]string) {
	t.Helper()
	previousServer, previousImages := askServerPlatform, askImagePlatforms
	askServerPlatform = func() ServerPlatform { return server }
	askImagePlatforms = func(image string) ([]string, error) {
		if platforms, ok := images[image]; ok {
			return platforms, nil
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { askServerPlatform, askImagePlatforms = previousServer, previousImages })
}

func TestAnImageForAnotherProcessorIsSaidBeforeThePull(t *testing.T) {
	withSensitiveFakes(t, map[string]bool{})
	previousExists := containerExists
	containerExists = func(string) bool { return false }
	t.Cleanup(func() { containerExists = previousExists })
	withPlatforms(t, ServerPlatform{OS: "linux", Arch: "amd64"}, map[string][]string{
		"arm-only:1":  {"linux/arm/v7", "linux/arm64/v8"},
		"multi:1":     {"linux/amd64", "linux/arm64"},
		"empty:1":     {},
	})
	server, answers := CheckImagePlatforms([]string{"arm-only:1", "multi:1", "empty:1", "private:1"})
	fits := []string{}
	for _, answer := range answers {
		fits = append(fits, answer.Fits)
	}
	if server.Arch != "amd64" || strings.Join(fits, ",") != "no,yes,unknown,unknown" {
		t.Errorf("%+v %v", server, fits)
	}

	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"app": {Image: "arm-only:1"}, "db": {Image: "multi:1"}}}
	recorder := httptest.NewRecorder()
	if forkRefuseService(recorder, httptest.NewRequest("POST", "/cosmos/api/docker-service", nil), request, func() {}) {
		t.Fatal("an arm-only image passed on amd64")
	}
	if body := recorder.Body.String(); !strings.Contains(body, "arm-only:1 is made for linux/arm/v7, linux/arm64/v8 only") || !strings.Contains(body, "linux/amd64") {
		t.Errorf("answer: %s", body)
	}
	if !forkRefuseService(httptest.NewRecorder(), httptest.NewRequest("POST", "/cosmos/api/docker-service?anyArch=1", nil), request, func() {}) {
		t.Error("\"install anyway\" was refused")
	}
	if normalArch("x86_64") != "amd64" || normalArch("aarch64") != "arm64" || normalArch("armv7l") != "arm" {
		t.Error("architecture names")
	}
}

func TestAContainerOnAnotherAppsNetworkDoesNotWaitForIt(t *testing.T) {
	// "use as VPN" with a VPN container that exists already: upstream waited for it forever
	order, _, err := ReOrderServices(map[string]ContainerCreateRequestContainer{
		"torrent": {Name: "torrent", NetworkMode: "container:gluetun"},
	})
	if err != nil || len(order) != 1 {
		t.Fatalf("%v %v", order, err)
	}
	// within one request the order still follows the network
	order, _, err = ReOrderServices(map[string]ContainerCreateRequestContainer{
		"torrent": {Name: "torrent", NetworkMode: "container:vpn"},
		"vpn":     {Name: "vpn"},
	})
	if err != nil || len(order) != 2 || order[0].Name != "vpn" {
		t.Fatalf("%+v %v", order, err)
	}
}
