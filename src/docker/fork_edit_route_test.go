package docker

// Fork addition (plan 47, I2c, review): the update route itself, through a
// stand-in Docker API: a changed value the edit check refuses never reaches
// Docker (the route calls the check before the tick and before the edit).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/gorilla/mux"
)

// fakeDockerAPI answers the ping and the inspect of one container; every other
// call is recorded (and fails), so that a test sees what the route asked for.
type fakeDockerAPI struct {
	mu        sync.Mutex
	container types.ContainerJSON
	others    []string
	// lenient: images exist and stop, rename, remove succeed, so that the edit reaches the creation
	lenient bool
	created [][]byte
}

func (f *fakeDockerAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Api-Version", "1.45")
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	if i := strings.Index(path, "/containers/"); i >= 0 && strings.HasSuffix(path, "/json") && r.Method == http.MethodGet {
		json.NewEncoder(w).Encode(f.container)
		return
	}
	if strings.HasSuffix(path, "/_ping") {
		w.Write([]byte("OK"))
		return
	}
	f.mu.Lock()
	f.others = append(f.others, r.Method+" "+path)
	if strings.HasSuffix(path, "/containers/create") {
		body, _ := io.ReadAll(r.Body)
		f.created = append(f.created, body)
	}
	f.mu.Unlock()
	if f.lenient && (strings.Contains(path, "/images/") && r.Method == http.MethodGet) {
		w.Write([]byte(`{"Id":"sha256:0","Config":{}}`))
		return
	}
	if f.lenient && r.Method != http.MethodGet && !strings.HasSuffix(path, "/containers/create") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(`{"message":"not in the stand-in"}`))
}

func withFakeDocker(t *testing.T, c types.ContainerJSON) *fakeDockerAPI {
	t.Helper()
	fake := &fakeDockerAPI{container: c}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	old, oldCtx := DockerClient, DockerContext
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithVersion("1.45"))
	if err != nil {
		t.Fatal(err)
	}
	DockerClient, DockerContext = cli, context.Background()
	t.Cleanup(func() { DockerClient, DockerContext = old, oldCtx })
	return fake
}

func updateThroughRoute(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/cosmos/api/servapps/radarr/update", strings.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"containerId": "radarr"})
	admin := &utils.AuthContext{Nickname: "alice", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES}}
	req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, admin))
	w := httptest.NewRecorder()
	UpdateContainerRoute(w, req)
	return w
}

func TestTheUpdateRouteChecksBeforeDocker(t *testing.T) {
	withServer(t)
	c := ownersApp()
	c.ContainerJSONBase.ID = "f00d"
	c.ContainerJSONBase.State = &types.ContainerState{Running: true}
	fake := withFakeDocker(t, c)
	for name, body := range map[string]string{
		"a DNS name":           `{"dns":["dns.example.org"]}`,
		"a bind with ..":       `{"Volumes":[{"Type":"bind","Source":"/mnt/data/../../etc","Target":"/x"}]}`,
		"another's address":    `{"portBindings":{"80/tcp":[{"HostIp":"192.168.1.99","HostPort":"8080"}]}}`,
		"a device not in /dev": `{"devices":["/etc/shadow:/dev/x"]}`,
	} {
		w := updateThroughRoute(t, body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "DS025") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(fake.others) != 0 {
		t.Fatalf("Docker was asked to change something: %v", fake.others)
	}
	// an acceptable change goes on to the tick and the edit (which the stand-in refuses)
	if w := updateThroughRoute(t, `{"dns":["192.168.1.1"]}`); w.Code == http.StatusBadRequest {
		t.Fatalf("an acceptable change was refused: %s", w.Body.String())
	}
}
