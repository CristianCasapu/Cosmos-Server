package docker

// Fork addition: tests of the leftovers of interrupted edits
// (fork_leftovers.go), against the fake Docker of fork_safe_edit_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

const newID = "fedcba9876543210fedcba9876543210"

// fakeLeftovers adds to fakeDocker what finding leftovers needs: listing,
// inspecting (which, like Docker, also answers a name with a container whose
// ID starts with it) and renames that fail for one target name.
type fakeLeftovers struct {
	*fakeDocker
	finished     map[string]string // by ID
	failRenameTo map[string]error  // by the name asked for
}

func (f *fakeLeftovers) Rename(id string, name string) error {
	if err := f.failRenameTo[name]; err != nil {
		f.calls = append(f.calls, "rename "+id+" "+name)
		return err
	}
	return f.fakeDocker.Rename(id, name)
}

func (f *fakeLeftovers) Names() ([]namedContainer, error) {
	if err := f.fail["list"]; err != nil {
		return nil, err
	}
	out := []namedContainer{}
	for id, c := range f.containers {
		out = append(out, namedContainer{ID: id, Name: c.name})
	}
	return out, nil
}

func (f *fakeLeftovers) Inspect(nameOrID string) (types.ContainerJSON, error) {
	for id, c := range f.containers {
		if c.name == nameOrID || id == nameOrID {
			return f.inspected(id, c), nil
		}
	}
	for id, c := range f.containers {
		if strings.HasPrefix(id, nameOrID) {
			return f.inspected(id, c), nil
		}
	}
	return types.ContainerJSON{}, errors.New("Error response from daemon: No such container: " + nameOrID)
}

func (f *fakeLeftovers) inspected(id string, c *fakeContainer) types.ContainerJSON {
	status := "exited"
	if c.running {
		status = "running"
	}
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:         id,
			Name:       "/" + c.name,
			Created:    "2026-11-01T10:00:00.123456789Z",
			State:      &types.ContainerState{Running: c.running, Status: status, FinishedAt: f.finished[id]},
			HostConfig: &conttype.HostConfig{},
		},
		Config: &conttype.Config{},
	}
}

// leftoverName is the name an edit gives the container with that ID when it
// sets it aside.
func leftoverName(app string, id string) string {
	return asideNameFor(app, id)
}

// useFakeLeftovers replaces Docker by a fake holding these containers (ID →
// name, running).
func useFakeLeftovers(t *testing.T, containers map[string]*fakeContainer) *fakeLeftovers {
	t.Helper()
	fake := &fakeLeftovers{
		fakeDocker:   &fakeDocker{containers: containers, fail: map[string]error{}},
		finished:     map[string]string{},
		failRenameTo: map[string]error{},
	}
	previous, previousDelay, previousInspector := swapper, renameRetryDelay, leftoverDocker
	swapper, renameRetryDelay, leftoverDocker = fake, 0, fake
	t.Cleanup(func() { swapper, renameRetryDelay, leftoverDocker = previous, previousDelay, previousInspector })
	return fake
}

func (f *fakeLeftovers) nameOf(id string) string {
	if c := f.containers[id]; c != nil {
		return c.name
	}
	return "<gone>"
}

func TestLeftoversPattern(t *testing.T) {
	for _, good := range []struct{ name, app, short string }{
		{"jellyfin-cosmos-old-1a2b3c4d5e6f", "jellyfin", "1a2b3c4d5e6f"},
		{"my_app.v2-cosmos-old-000000000000", "my_app.v2", "000000000000"},
		{"a-cosmos-old-cosmos-old-abcdefabcdef", "a-cosmos-old", "abcdefabcdef"},
	} {
		app, short, ok := parseLeftoverName(good.name)
		if !ok || app != good.app || short != good.short {
			t.Errorf("%s: got %q %q %v", good.name, app, short, ok)
		}
	}
	for _, bad := range []string{
		"", "jellyfin", "jellyfin-cosmos-old-", "-cosmos-old-1a2b3c4d5e6f",
		"jellyfin-cosmos-old-1A2B3C4D5E6F", "jellyfin-cosmos-old-1a2b3c4d5e6", "jellyfin-cosmos-old-1a2b3c4d5e6f0",
		"jellyfin-cosmos-old-1a2b3c4d5e6g", "/jellyfin-cosmos-old-1a2b3c4d5e6f", "jelly fin-cosmos-old-1a2b3c4d5e6f",
		"jellyfin-cosmos-old-1a2b3c4d5e6f\n", "jelly\nfin-cosmos-old-1a2b3c4d5e6f", "../x-cosmos-old-1a2b3c4d5e6f",
		strings.Repeat("a", 300) + "-cosmos-old-1a2b3c4d5e6f",
	} {
		if _, _, ok := parseLeftoverName(bad); ok {
			t.Errorf("%q taken for a leftover", bad)
		}
	}

	if !isLeftover("app-cosmos-old-0123456789ab", oldID) {
		t.Error("the leftover of its own ID is not recognised")
	}
	if isLeftover("app-cosmos-old-0123456789ab", newID) {
		t.Error("a container whose ID does not start with the hex of its name is taken for a leftover")
	}
	if isLeftover("app-cosmos-old-0123456789ab", "0123456789a") {
		t.Error("a short ID is taken for a leftover")
	}
}

func TestLeftoversList(t *testing.T) {
	const otherID = "aaaaaaaaaaaa00000000000000000000"
	const thirdID = "bbbbbbbbbbbb00000000000000000000"
	fake := useFakeLeftovers(t, map[string]*fakeContainer{
		oldID:   {name: leftoverName("app", oldID)},
		newID:   {name: "app", running: true},
		otherID: {name: "fake-cosmos-old-cccccccccccc"}, // the hex are not its ID's
		thirdID: {name: leftoverName("gone", thirdID)},
	})
	fake.finished[oldID] = "2026-11-10T08:30:00.5Z"
	fake.finished[thirdID] = "0001-01-01T00:00:00Z"

	found, err := ListLeftovers()
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("found %+v", found)
	}
	want := Leftover{Name: "app-cosmos-old-0123456789ab", App: "app", ID: "0123456789ab", State: "exited",
		Created: "2026-11-01T10:00:00Z", Finished: "2026-11-10T08:30:00Z", AppExists: true, AppRunning: true}
	if found[0] != want {
		t.Errorf("first = %+v, want %+v", found[0], want)
	}
	if found[1].App != "gone" || found[1].AppExists || found[1].Finished != "" {
		t.Errorf("second = %+v", found[1])
	}
	if len(fake.calls) != 0 {
		t.Errorf("listing changed something: %v", fake.calls)
	}

	fake.fail["list"] = errors.New("Cannot connect to the Docker daemon")
	if _, err := ListLeftovers(); err == nil {
		t.Error("a Docker that does not answer gives an empty list")
	}
}

func TestLeftoversRestoreWithoutReplacement(t *testing.T) {
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: leftoverName("app", oldID)}})

	result, err := restoreLeftover(leftoverName("app", oldID))
	if err != nil {
		t.Fatal(err)
	}
	if result.App != "app" || result.SetAside != "" {
		t.Errorf("result %+v", result)
	}
	if c := fake.containers[oldID]; c.name != "app" || !c.running {
		t.Errorf("the previous container is %+v", *c)
	}
	if fake.called("remove") || fake.called("stop") {
		t.Errorf("calls %v", fake.calls)
	}
}

func TestLeftoversRestoreWithReplacement(t *testing.T) {
	fake := useFakeLeftovers(t, map[string]*fakeContainer{
		oldID: {name: leftoverName("app", oldID)},
		newID: {name: "app", running: true},
	})

	result, err := restoreLeftover(leftoverName("app", oldID))
	if err != nil {
		t.Fatal(err)
	}
	if result.SetAside != leftoverName("app", newID) {
		t.Errorf("result %+v", result)
	}
	if c := fake.containers[oldID]; c.name != "app" || !c.running {
		t.Errorf("the previous container is %+v", *c)
	}
	// the replacement is set aside the way an edit does: stopped, renamed, kept
	if c := fake.containers[newID]; c == nil || c.name != "app-cosmos-old-fedcba987654" || c.running {
		t.Errorf("the replacement is %+v", c)
	}
	if fake.called("remove") {
		t.Errorf("something was removed: %v", fake.calls)
	}
	want := []string{"stop " + newID, "rename " + newID + " app-cosmos-old-fedcba987654", "rename " + oldID + " app", "start " + oldID}
	if strings.Join(fake.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls %v, want %v", fake.calls, want)
	}
	// and it is a leftover itself now
	found, _ := ListLeftovers()
	if len(found) != 1 || found[0].Name != "app-cosmos-old-fedcba987654" || !found[0].AppRunning {
		t.Errorf("leftovers now %+v", found)
	}
}

func TestLeftoversRestoreRollsBack(t *testing.T) {
	name := leftoverName("app", oldID)

	// the previous container does not start: both get their names back, the replacement runs again
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	fake.fail["start:"+oldID] = errors.New("port is already allocated")
	_, err := restoreLeftover(name)
	var failed *restoreError
	if !errors.As(err, &failed) || !failed.undone || !strings.Contains(err.Error(), "port is already allocated") {
		t.Fatalf("error %v", err)
	}
	if fake.nameOf(oldID) != name || fake.containers[oldID].running || fake.nameOf(newID) != "app" || !fake.containers[newID].running {
		t.Errorf("after the rollback: %s %+v, %s %+v", fake.nameOf(oldID), fake.containers[oldID], fake.nameOf(newID), fake.containers[newID])
	}

	// the previous container cannot take the name: the replacement gets it back
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	fake.failRenameTo["app"] = errors.New("Error response from daemon: something")
	// (the replacement's own rename back to "app" fails as well here, which is the case where not all is undone)
	_, err = restoreLeftover(name)
	if !errors.As(err, &failed) || failed.undone {
		t.Fatalf("error %v", err)
	}
	if !strings.Contains(err.Error(), "kept, stopped, as app-cosmos-old-fedcba987654") {
		t.Errorf("the error does not say where the replacement is: %v", err)
	}

	// the same, the name being free again for the way back
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	fake.fail["rename:"+oldID] = errors.New("Error response from daemon: something")
	_, err = restoreLeftover(name)
	if !errors.As(err, &failed) || !failed.undone {
		t.Fatalf("error %v", err)
	}
	if fake.nameOf(oldID) != name || fake.nameOf(newID) != "app" || !fake.containers[newID].running {
		t.Errorf("after the rollback: %s, %s %+v", fake.nameOf(oldID), fake.nameOf(newID), fake.containers[newID])
	}

	// the start fails and the previous container cannot get its leftover name back:
	// the replacement stays aside (its name is taken), and the error says so
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	fake.fail["start:"+oldID] = errors.New("no")
	fake.failRenameTo[name] = errors.New("Error response from daemon: busy")
	_, err = restoreLeftover(name)
	if !errors.As(err, &failed) || failed.undone || !strings.Contains(err.Error(), "leftover name back") {
		t.Fatalf("error %v", err)
	}
	if fake.called("remove") {
		t.Errorf("something was removed: %v", fake.calls)
	}

	// the replacement cannot be set aside: nothing is changed
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	fake.fail["stop:"+newID] = errors.New("cannot stop")
	_, err = restoreLeftover(name)
	if !errors.As(err, &failed) || !failed.undone {
		t.Fatalf("error %v", err)
	}
	if fake.nameOf(oldID) != name || fake.nameOf(newID) != "app" || fake.called("rename") {
		t.Errorf("calls %v", fake.calls)
	}
}

func TestLeftoversActionsTouchLeftoversOnly(t *testing.T) {
	// a name of the pattern whose hex are not the container's ID
	fake := useFakeLeftovers(t, map[string]*fakeContainer{newID: {name: "app-cosmos-old-0123456789ab"}, oldID: {name: "app", running: true}})
	if _, err := restoreLeftover("app-cosmos-old-0123456789ab"); !errors.Is(err, errNotLeftover) {
		t.Errorf("restore: %v", err)
	}
	if err := removeLeftover("app-cosmos-old-0123456789ab"); !errors.Is(err, errNotLeftover) {
		t.Errorf("remove: %v", err)
	}
	// a name of the pattern that no container has
	if err := removeLeftover("fedcba-cosmos-old-fedcba987654"); !errors.Is(err, errNotLeftover) {
		t.Errorf("remove of a missing name: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("calls %v", fake.calls)
	}

	// the app's name answered by Docker with another container (its ID starts with the name):
	// that container is not taken for the app and not set aside
	const hexApp = "fedcba"
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: leftoverName(hexApp, oldID)}, newID: {name: "unrelated", running: true}})
	result, err := restoreLeftover(leftoverName(hexApp, oldID))
	if err != nil || result.SetAside != "" {
		t.Fatalf("%+v %v", result, err)
	}
	if fake.nameOf(newID) != "unrelated" || !fake.containers[newID].running || fake.nameOf(oldID) != hexApp {
		t.Errorf("calls %v", fake.calls)
	}
}

func TestLeftoversRemove(t *testing.T) {
	name := leftoverName("app", oldID)
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name, running: true}, newID: {name: "app", running: true}})
	if err := removeLeftover(name); !errors.Is(err, errLeftoverRunning) {
		t.Errorf("a running leftover: %v", err)
	}
	if fake.called("remove") || fake.called("stop") {
		t.Errorf("calls %v", fake.calls)
	}

	fake.containers[oldID].running = false
	if err := removeLeftover(name); err != nil {
		t.Fatal(err)
	}
	if fake.containers[oldID] != nil || fake.containers[newID] == nil || !fake.containers[newID].running {
		t.Errorf("after: %+v", fake.containers)
	}
	if strings.Join(fake.calls, "|") != "remove "+oldID {
		t.Errorf("calls %v", fake.calls)
	}

	if err := removeLeftover(name); !errors.Is(err, errNotLeftover) {
		t.Errorf("removed twice: %v", err)
	}
}

func TestLeftoversReportedOnceAtStart(t *testing.T) {
	const thirdID = "bbbbbbbbbbbb00000000000000000000"
	useFakeLeftovers(t, map[string]*fakeContainer{
		oldID:   {name: leftoverName("app", oldID)},
		thirdID: {name: leftoverName("other", thirdID)},
		newID:   {name: "app", running: true},
	})
	written := []utils.Notification{}
	known := map[string]bool{}
	previousWrite, previousNotified := writeNotification, notifiedBefore
	writeNotification = func(n utils.Notification) {
		written = append(written, n)
		known[n.Vars] = true
	}
	notifiedBefore = func(vars string) bool { return known[vars] }
	t.Cleanup(func() { writeNotification, notifiedBefore = previousWrite, previousNotified })

	if count := reportLeftovers(); count != 2 {
		t.Errorf("%d reported", count)
	}
	if len(written) != 2 {
		t.Fatalf("written %+v", written)
	}
	for _, n := range written {
		if n.Recipient != "admin" || n.Title != leftoverNotificationTitle || n.Message != leftoverNotificationMessage || n.Link != "/cosmos-ui/servapps" {
			t.Errorf("notification %+v", n)
		}
	}
	if written[0].Vars != "app (kept as app-cosmos-old-0123456789ab)" && written[1].Vars != "app (kept as app-cosmos-old-0123456789ab)" {
		t.Errorf("notifications %+v", written)
	}

	// a second start tells nothing new
	reportLeftovers()
	if len(written) != 2 {
		t.Errorf("told again: %+v", written)
	}
}

func TestLeftoversLogSafe(t *testing.T) {
	if got := logSafe("a\nb\rc d\x1b[31m"); got != "a?b?c?d??31m" {
		t.Errorf("%q", got)
	}
	if got := logSafe(strings.Repeat("x", 500)); len(got) != 128 {
		t.Errorf("%d", len(got))
	}
}

// ---- route ------------------------------------------------------------------

// leftoverRequestAs builds a request from nobody ("none"), a user without the
// permission ("user"), a reader of the containers ("reader"), a manager of
// them who has not confirmed the password ("manager") or one who has ("sudo").
func leftoverRequestAs(method string, who string, body string) *http.Request {
	req := httptest.NewRequest(method, "http://cosmos.example/cosmos/api/servapps-leftovers", strings.NewReader(body))
	if who == "none" {
		return req
	}
	auth := &utils.AuthContext{Nickname: "alice", Permissions: []utils.Permission{utils.PERM_LOGIN}, IsSudoed: who == "sudo"}
	switch who {
	case "reader":
		auth.Permissions = append(auth.Permissions, utils.PERM_RESOURCES_READ)
	case "manager", "sudo":
		auth.Permissions = append(auth.Permissions, utils.PERM_RESOURCES_READ, utils.PERM_RESOURCES)
	}
	return req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
}

func callLeftovers(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	LeftoversRoute(rec, req)
	return rec
}

func answerCode(rec *httptest.ResponseRecorder) string {
	var answer struct{ Code string }
	json.Unmarshal(rec.Body.Bytes(), &answer)
	return answer.Code
}

func captureLeftoverSecurity(t *testing.T) func() []utils.SecurityEvent {
	t.Helper()
	var mu sync.Mutex
	events := []utils.SecurityEvent{}
	utils.SetSecurityRecorder(func(event utils.SecurityEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}, func() {})
	t.Cleanup(func() { utils.SetSecurityRecorder(nil, nil) })
	return func() []utils.SecurityEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]utils.SecurityEvent(nil), events...)
	}
}

func TestLeftoversRoutePermissionsAndMethods(t *testing.T) {
	name := leftoverName("app", oldID)
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	recorded := captureLeftoverSecurity(t)
	body := `{"action":"restore","name":"` + name + `"}`

	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		if rec := callLeftovers(leftoverRequestAs(method, "none", body)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", method, rec.Code)
		}
		if rec := callLeftovers(leftoverRequestAs(method, "user", body)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without the permission: %d", method, rec.Code)
		}
	}
	for _, method := range []string{"PUT", "PATCH", "DELETE", "HEAD"} {
		if rec := callLeftovers(leftoverRequestAs(method, "sudo", body)); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", method, rec.Code)
		}
	}
	// acting takes the permission to manage containers, password confirmed
	for _, who := range []string{"reader", "manager"} {
		if rec := callLeftovers(leftoverRequestAs("POST", who, body)); rec.Code != http.StatusUnauthorized {
			t.Errorf("POST as %s: %d", who, rec.Code)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("refused requests changed something: %v", fake.calls)
	}
	for _, event := range recorded() {
		if event.Kind == utils.SecContainerAction || event.Kind == utils.SecContainerDeleted {
			t.Errorf("a refused request was recorded as an action: %+v", event)
		}
	}

	// reading takes the permission to see them
	rec := callLeftovers(leftoverRequestAs("GET", "reader", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET as reader: %d %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Status string
		Data   []Leftover
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || listed.Status != "OK" || len(listed.Data) != 1 || listed.Data[0].Name != name {
		t.Errorf("GET answered %s", rec.Body.String())
	}

	fake.fail["list"] = errors.New("down")
	if rec := callLeftovers(leftoverRequestAs("GET", "reader", "")); rec.Code != http.StatusServiceUnavailable || answerCode(rec) != "LEFT007" {
		t.Errorf("GET with Docker down: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLeftoversRouteBodies(t *testing.T) {
	name := leftoverName("app", oldID)
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}, newID: {name: "app", running: true}})
	captureLeftoverSecurity(t)

	for _, bad := range []struct{ body, code string }{
		{``, "LEFT001"},
		{`not json`, "LEFT001"},
		{`{"action":"restore","name":"` + name + `","confirm":true}`, "LEFT001"},
		{`{"action":"restore","name":"` + name + `"} {"action":"remove"}`, "LEFT001"},
		{`{"action":"restore","name":"` + name + `","pad":"` + strings.Repeat("x", 1100) + `"}`, "LEFT001"},
		{`{"action":"restore","name":"` + strings.Repeat("a", 1100) + `-cosmos-old-0123456789ab"}`, "LEFT001"},
		{`{"action":"delete","name":"` + name + `"}`, "LEFT001"},
		{`{"action":"remove","name":"app"}`, "LEFT002"},
		{`{"action":"remove","name":"app-cosmos-old-fedcba987654"}`, "LEFT002"},
	} {
		rec := callLeftovers(leftoverRequestAs("POST", "sudo", bad.body))
		if code := answerCode(rec); code != bad.code {
			t.Errorf("%.80s: %d %s, want %s", bad.body, rec.Code, rec.Body.String(), bad.code)
		}
	}
	if len(fake.calls) != 0 {
		t.Errorf("bad requests changed something: %v", fake.calls)
	}
}

func TestLeftoversRouteActions(t *testing.T) {
	name := leftoverName("app", oldID)
	fake := useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name, running: true}, newID: {name: "app", running: true}})
	recorded := captureLeftoverSecurity(t)

	// remove refused while it runs
	rec := callLeftovers(leftoverRequestAs("POST", "sudo", `{"action":"remove","name":"`+name+`"}`))
	if rec.Code != http.StatusConflict || answerCode(rec) != "LEFT003" || fake.containers[oldID] == nil {
		t.Errorf("remove of a running leftover: %d %s", rec.Code, rec.Body.String())
	}

	// restore
	fake.containers[oldID].running = false
	rec = callLeftovers(leftoverRequestAs("POST", "sudo", `{"action":"restore","name":"`+name+`"}`))
	var answer struct {
		Status string
		Data   map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil || rec.Code != http.StatusOK || answer.Data["result"] != "restored" ||
		answer.Data["app"] != "app" || answer.Data["setAside"] != "app-cosmos-old-fedcba987654" {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}

	// the replacement, now a leftover, removed
	rec = callLeftovers(leftoverRequestAs("POST", "sudo", `{"action":"remove","name":"app-cosmos-old-fedcba987654"}`))
	if rec.Code != http.StatusOK || fake.containers[newID] != nil || fake.nameOf(oldID) != "app" {
		t.Errorf("remove: %d %s", rec.Code, rec.Body.String())
	}

	// a failed restore that was undone, and one that was not
	fake = useFakeLeftovers(t, map[string]*fakeContainer{oldID: {name: name}})
	fake.fail["start:"+oldID] = errors.New("port is already allocated")
	rec = callLeftovers(leftoverRequestAs("POST", "sudo", `{"action":"restore","name":"`+name+`"}`))
	if rec.Code != http.StatusInternalServerError || answerCode(rec) != "LEFT004" || !strings.Contains(rec.Body.String(), "port is already allocated") {
		t.Errorf("failed restore: %d %s", rec.Code, rec.Body.String())
	}
	fake.failRenameTo[name] = errors.New("busy")
	rec = callLeftovers(leftoverRequestAs("POST", "sudo", `{"action":"restore","name":"`+name+`"}`))
	if answerCode(rec) != "LEFT005" {
		t.Errorf("failed restore not undone: %d %s", rec.Code, rec.Body.String())
	}

	kinds := []string{}
	for _, event := range recorded() {
		kinds = append(kinds, event.Kind+">"+event.Target+" "+event.Details["action"])
	}
	want := []string{
		utils.SecContainerDeleted + ">" + name + " ",
		utils.SecContainerAction + ">" + name + " restore_leftover",
		utils.SecContainerDeleted + ">app-cosmos-old-fedcba987654 ",
		utils.SecContainerAction + ">" + name + " restore_leftover",
		utils.SecContainerAction + ">" + name + " restore_leftover",
	}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Errorf("recorded %v, want %v", kinds, want)
	}
}
