package docker

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

// fakeDocker is the little of Docker that setting a container aside uses:
// containers with a unique name that run or not.
type fakeDocker struct {
	containers map[string]*fakeContainer // by ID
	fail       map[string]error          // by call, "stop", "start", "rename" or "remove", optionally ":<id>"
	nameBusy   int                       // the next renames answer that the name is taken, this many times
	calls      []string
}

type fakeContainer struct {
	name    string
	running bool
}

func (d *fakeDocker) failure(call string, id string) error {
	if err := d.fail[call+":"+id]; err != nil {
		return err
	}
	return d.fail[call]
}

func (d *fakeDocker) Stop(id string) error {
	d.calls = append(d.calls, "stop "+id)
	if err := d.failure("stop", id); err != nil {
		return err
	}
	c := d.containers[id]
	if c == nil {
		return errors.New("Error response from daemon: No such container: " + id)
	}
	c.running = false
	return nil
}

func (d *fakeDocker) Start(id string) error {
	d.calls = append(d.calls, "start "+id)
	if err := d.failure("start", id); err != nil {
		return err
	}
	c := d.containers[id]
	if c == nil {
		return errors.New("Error response from daemon: No such container: " + id)
	}
	c.running = true
	return nil
}

func (d *fakeDocker) Rename(id string, name string) error {
	d.calls = append(d.calls, "rename "+id+" "+name)
	if err := d.failure("rename", id); err != nil {
		return err
	}
	c := d.containers[id]
	if c == nil {
		return errors.New("Error response from daemon: No such container: " + id)
	}
	if d.nameBusy > 0 {
		d.nameBusy--
		return errors.New("Error response from daemon: Conflict. The container name \"/" + name + "\" is already in use")
	}
	for otherID, other := range d.containers {
		if otherID != id && other.name == name {
			return errors.New("Error response from daemon: Conflict. The container name \"/" + name + "\" is already in use")
		}
	}
	c.name = name
	return nil
}

func (d *fakeDocker) Remove(id string, force bool) error {
	d.calls = append(d.calls, "remove "+id)
	if err := d.failure("remove", id); err != nil {
		return err
	}
	c := d.containers[id]
	if c == nil {
		return errors.New("Error response from daemon: No such container: " + id)
	}
	if c.running && !force {
		return errors.New("Error response from daemon: cannot remove container: container is running")
	}
	delete(d.containers, id)
	return nil
}

func (d *fakeDocker) IDByName(name string) string {
	for id, c := range d.containers {
		if c.name == name {
			return id
		}
	}
	return ""
}

func (d *fakeDocker) called(prefix string) bool {
	for _, call := range d.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

const oldID = "0123456789abcdef0123456789abcdef"

// useFakeDocker replaces Docker by a fake that holds one container, "app".
func useFakeDocker(t *testing.T, running bool) (*fakeDocker, types.ContainerJSON) {
	t.Helper()
	fake := &fakeDocker{
		containers: map[string]*fakeContainer{oldID: {name: "app", running: running}},
		fail:       map[string]error{},
	}
	previous, previousDelay := swapper, renameRetryDelay
	swapper, renameRetryDelay = fake, 0
	t.Cleanup(func() { swapper, renameRetryDelay = previous, previousDelay })

	inspected := types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:         oldID,
			Name:       "/app",
			State:      &types.ContainerState{Running: running},
			HostConfig: &conttype.HostConfig{},
		},
		Config: &conttype.Config{},
	}
	return fake, inspected
}

func TestSetAsideKeepsTheContainer(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)

	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}

	old := fake.containers[oldID]
	if old == nil {
		t.Fatal("the container must still exist while its replacement is made")
	}
	if old.running {
		t.Error("the container must be stopped, its ports are needed by the replacement")
	}
	if old.name != "app-cosmos-old-0123456789ab" || aside.asideName != old.name {
		t.Errorf("the container must be kept under a name of its own, got %q", old.name)
	}
	if fake.IDByName("app") != "" {
		t.Error("the name must be free for the replacement")
	}
	if fake.called("remove") {
		t.Error("nothing may be removed before the replacement runs")
	}
}

func TestFailedEditPutsTheContainerBack(t *testing.T) {
	for _, wasRunning := range []bool{true, false} {
		fake, inspected := useFakeDocker(t, wasRunning)
		aside, err := setContainerAside(inspected)
		if err != nil {
			t.Fatal(err)
		}

		// creating the replacement failed: there is none
		id, err := aside.revert("", errors.New("Minimum memory limit allowed is 6MB"), nil)

		if err == nil || !strings.Contains(err.Error(), "Minimum memory limit allowed is 6MB") {
			t.Errorf("the reason of the failure must reach the caller, got %v", err)
		}
		if id != oldID {
			t.Errorf("the container that is back must be reported, got %q", id)
		}
		old := fake.containers[oldID]
		if old == nil || old.name != "app" {
			t.Fatalf("the container must be back under its name, got %+v", old)
		}
		if old.running != wasRunning {
			t.Errorf("running before the edit: %v, after the failed edit: %v", wasRunning, old.running)
		}
	}
}

func TestFailedStartRemovesTheReplacement(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}
	fake.containers["new"] = &fakeContainer{name: "app"}

	_, err = aside.revert("new", nil, errors.New("Bind for 0.0.0.0:80 failed: port is already allocated"))

	if err == nil || !strings.Contains(err.Error(), "port is already allocated") {
		t.Errorf("the reason of the failure must reach the caller, got %v", err)
	}
	if fake.containers["new"] != nil {
		t.Error("the replacement that could not be started must be removed")
	}
	old := fake.containers[oldID]
	if old == nil || old.name != "app" || !old.running {
		t.Errorf("the container must be back under its name and running, got %+v", old)
	}
}

func TestPutBackWaitsForTheName(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}
	fake.nameBusy = 3

	if err := aside.putBack(""); err != nil {
		t.Fatalf("a name that is released a moment later must not fail the way back: %v", err)
	}
	if old := fake.containers[oldID]; old.name != "app" || !old.running {
		t.Errorf("the container must be back under its name and running, got %+v", old)
	}
}

func TestReplacementThatCannotBeRemoved(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}
	fake.containers["new"] = &fakeContainer{name: "app"}
	fake.fail["remove:new"] = errors.New("device or resource busy")

	id, err := aside.revert("new", nil, errors.New("cannot start"))

	if err == nil || !strings.Contains(err.Error(), "cannot start") || !strings.Contains(err.Error(), aside.asideName) {
		t.Errorf("the error must give the reason and say where the previous container is, got %v", err)
	}
	if id != "" {
		t.Errorf("nothing was restored, got %q", id)
	}
	if old := fake.containers[oldID]; old == nil || old.name != aside.asideName {
		t.Errorf("the previous container must be kept, got %+v", old)
	}
}

func TestSetAsideFailureLeavesTheContainerRunning(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	fake.fail["rename"] = errors.New("rename refused")

	aside, err := setContainerAside(inspected)

	if err == nil || aside != nil {
		t.Fatalf("a container that cannot be set aside must not be edited, got %v", err)
	}
	if old := fake.containers[oldID]; old == nil || old.name != "app" || !old.running {
		t.Errorf("the container must be as it was, running under its name, got %+v", old)
	}

	fake, inspected = useFakeDocker(t, true)
	fake.fail["stop"] = errors.New("stop refused")
	if _, err := setContainerAside(inspected); err == nil {
		t.Fatal("a container that cannot be stopped must not be edited")
	}
	if fake.called("rename") || fake.called("remove") {
		t.Errorf("nothing may be changed when the container cannot be stopped, calls: %v", fake.calls)
	}
}

func TestSetAsideRefusesARunningAutoRemoveContainer(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	inspected.HostConfig.AutoRemove = true

	if _, err := setContainerAside(inspected); err == nil {
		t.Fatal("stopping a container started with --rm removes it: the edit must be refused")
	}
	if len(fake.calls) != 0 {
		t.Errorf("the container must not be touched, calls: %v", fake.calls)
	}
}

func TestDropRemovesTheOldContainerOnce(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}
	fake.containers["new"] = &fakeContainer{name: "app", running: true}

	aside.drop()
	if fake.containers[oldID] != nil {
		t.Error("the old container must be removed once its replacement runs")
	}

	// whatever is asked afterwards, the replacement stays
	aside.drop()
	if err := aside.putBack("new"); err != nil {
		t.Error(err)
	}
	if c := fake.containers["new"]; c == nil || c.name != "app" || !c.running {
		t.Errorf("the replacement must be left alone after the edit succeeded, got %+v", c)
	}

	var none *asideContainer
	none.drop()
	dropAsides([]DockerServiceCreateRollback{{Action: "remove"}, {Action: "restore"}})
}

func TestPutBackNeverRemovesTheContainerItself(t *testing.T) {
	fake, inspected := useFakeDocker(t, true)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}

	if err := aside.putBack(oldID); err != nil {
		t.Fatal(err)
	}
	if old := fake.containers[oldID]; old == nil || old.name != "app" || !old.running {
		t.Fatalf("the container must be back, got %+v", old)
	}

	// a second rollback step for the same container finds it under its name
	rollbackPutBack(DockerServiceCreateRollback{Action: "restore", aside: aside}, func(string) {})
	if fake.containers[oldID] == nil {
		t.Fatal("a container that is back must never be removed as if it were the replacement")
	}
}

func TestServiceRollbackPutsTheContainerBack(t *testing.T) {
	fake, inspected := useFakeDocker(t, false)
	aside, err := setContainerAside(inspected)
	if err != nil {
		t.Fatal(err)
	}
	fake.containers["new"] = &fakeContainer{name: "app", running: true}

	logged := ""
	rollbackPutBack(DockerServiceCreateRollback{Action: "restore", aside: aside}, func(line string) { logged += line })

	if fake.containers["new"] != nil {
		t.Error("the replacement must be removed by the rollback")
	}
	old := fake.containers[oldID]
	if old == nil || old.name != "app" {
		t.Fatalf("the container must be back under its name, got %+v", old)
	}
	if old.running {
		t.Error("a container that was stopped before the update must not be started by the rollback")
	}
	if !strings.Contains(logged, "Rolled back container app") {
		t.Errorf("the rollback must be reported, got %q", logged)
	}
}

// between is the part of text from the first start to the first end after it.
func between(t *testing.T, text string, start string, end string) string {
	t.Helper()
	from := strings.Index(text, start)
	if from < 0 {
		t.Fatalf("%q not found", start)
	}
	length := strings.Index(text[from:], end)
	if length < 0 {
		t.Fatalf("%q not found after %q", end, start)
	}
	return text[from : from+length]
}

// The two places that replace a container must set the old one aside and may
// not remove anything before the replacement is created. This is what an
// upstream merge could undo without any other test noticing.
func TestNothingIsRemovedBeforeTheReplacementExists(t *testing.T) {
	source, err := os.ReadFile("docker.go")
	if err != nil {
		t.Fatal(err)
	}
	edit := between(t, string(source), "func EditContainer(", "DockerClient.ContainerCreate(")
	if !strings.Contains(edit, "setContainerAside(") {
		t.Error("EditContainer must set the old container aside")
	}
	if strings.Contains(edit, "ContainerRemove(") {
		t.Error("EditContainer must not remove the old container before its replacement is created")
	}
	rest := between(t, string(source), "func EditContainer(", "func RecreateDepedencies(")
	if !strings.Contains(rest, "aside.revert(") || !strings.Contains(rest, "aside.drop()") {
		t.Error("EditContainer must put the old container back on failure and remove it on success")
	}

	source, err = os.ReadFile("api_blueprint.go")
	if err != nil {
		t.Fatal(err)
	}
	update := between(t, string(source), "existingContainer, err := DockerClient.ContainerInspect(", "DockerClient.ContainerCreate(")
	if !strings.Contains(update, "setContainerAside(") || !strings.Contains(update, "aside: aside") {
		t.Error("a service update must set the existing container aside and remember it for the rollback")
	}
	if strings.Contains(update, "ContainerRemove(") {
		t.Error("a service update must not remove the existing container before its replacement is created")
	}
	if !strings.Contains(string(source), "rollbackPutBack(action, OnLog)") || !strings.Contains(string(source), "dropAsides(rollbackActions)") {
		t.Error("a service update must put the container back on rollback and remove it on success")
	}
}
