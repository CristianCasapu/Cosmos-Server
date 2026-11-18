package docker

// Fork addition (plan 47, I2c, second review): disguised volumes through
// links and existing volumes (S1), binds nested in folders apps can write
// (S2), links into another app's folder (M4), device permissions (M2), the
// keep through the route (read-only flag, a port's host address, M7), the
// bounds (M8), the new sensitive places and options (M5).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/azukaar/cosmos-server/src/edithints"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	containerType "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

// a server tree in a temporary folder: the data place, an app's folder with
// planted links (to /, to /srv, into another app's folder), another app's folder
func review2Server(t *testing.T, writers []writer, volumes map[string]map[string]string) string {
	t.Helper()
	withServer(t)
	root := t.TempDir()
	for _, d := range []string{"mnt/data/appA/config", "mnt/data/appB/secrets", "mnt/data/media/movies", "srv/x", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for at, target := range map[string]string{
		"mnt/data/appA/config/rootfs": "/",
		"mnt/data/appA/config/srv":    "/srv/x",
		"mnt/data/appA/config/theirs": "/mnt/data/appB/secrets",
	} {
		if err := os.Symlink(target, filepath.Join(root, at)); err != nil {
			t.Fatal(err)
		}
	}
	oldResolve, oldPlaces, oldList, oldLookup, oldKeys := resolveSource, EditDataPlaces, listServerMounts, lookupVolume, existingMountKeys
	t.Cleanup(func() {
		resolveSource, EditDataPlaces, listServerMounts, lookupVolume, existingMountKeys = oldResolve, oldPlaces, oldList, oldLookup, oldKeys
		resetEditCaches()
	})
	resetEditCaches()
	resolveSource = func(p string) (string, bool, error) { return edithints.ResolvePath(root, p) }
	EditDataPlaces = func() []string { return []string{"/mnt/data"} }
	listServerMounts = func() ([]writer, bool) { return append([]writer{}, writers...), true }
	lookupVolume = func(name string) (string, map[string]string, bool) {
		options, ok := volumes[name]
		return "local", options, ok
	}
	existingMountKeys = func(string) []string { return nil }
	return root
}

func resetEditCaches() {
	volumeFactsMu.Lock()
	volumeFacts = map[string]volumeFact{}
	volumeFactsMu.Unlock()
	serverMountsMu.Lock()
	serverMounts = nil
	serverMountsMu.Unlock()
}

func disguised(name, device string) mount.Mount {
	return mount.Mount{Type: mount.TypeVolume, Source: name, Target: "/x", VolumeOptions: &mount.VolumeOptions{DriverConfig: &mount.Driver{Name: "local", Options: map[string]string{"type": "none", "o": "bind", "device": device}}}}
}

func keysOf(items []SensitiveItem) string {
	keys := []string{}
	for _, i := range items {
		keys = append(keys, i.Key())
	}
	return strings.Join(keys, " ")
}

func editAsked(t *testing.T, before types.ContainerJSON, change func(*types.ContainerJSON)) (string, error) {
	t.Helper()
	after := edited(before, change)
	where, err := editAccess(before, &after)
	if err != nil {
		return "", err
	}
	return keysOf(unconfirmedEdit("radarr", before, after, url.Values{}, nil, where...)), nil
}

func createAsked(t *testing.T, request DockerServiceCreateRequest) (string, error) {
	t.Helper()
	where, err := serviceAccess(request, false)
	if err != nil {
		return "", err
	}
	return keysOf(unconfirmedSensitive(request, url.Values{}, where...)), nil
}

func TestDisguisedVolumesAreFollowed(t *testing.T) {
	review2Server(t, nil, map[string]map[string]string{"v9": {"type": "none", "o": "bind", "device": "/etc"}})
	before := ownersApp()
	// a device through a planted link to /: rewritten and ticked as /, on the edit and on the creation
	got, err := editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, disguised("v1", "/mnt/data/appA/config/rootfs"))
	})
	if err != nil || got != "radarr|bind|/" {
		t.Errorf("edit, device through a link: %q %v", got, err)
	}
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"app": {Volumes: []mount.Mount{disguised("v1", "/mnt/data/appA/config/rootfs")}}}}
	got, err = createAsked(t, request)
	if err != nil || got != "app|bind|/" || request.Services["app"].Volumes[0].VolumeOptions.DriverConfig.Options["device"] != "/" {
		t.Errorf("creation, device through a link: %q %v", got, err)
	}
	// a device through a link to a folder that is neither data nor sensitive: refused in words
	if _, err := editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, disguised("v2", "/mnt/data/appA/config/srv"))
	}); err == nil || !strings.Contains(err.Error(), "is a link to /srv/x") {
		t.Errorf("a link out: %v", err)
	}
	// an existing volume made with bind options, named without options: the folder it binds
	got, err = editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "v9", Target: "/h"})
	})
	if err != nil || got != "radarr|bind|/etc" {
		t.Errorf("edit, existing volume: %q %v", got, err)
	}
	got, err = createAsked(t, DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"app": {Volumes: []mount.Mount{{Type: mount.TypeVolume, Source: "v9", Target: "/h"}}}}})
	if err != nil || got != "app|bind|/etc" {
		t.Errorf("creation, existing volume: %q %v", got, err)
	}
	// an ordinary volume asks nothing
	if got, _ := editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "plain", Target: "/p"})
	}); got != "" {
		t.Errorf("an ordinary volume: %q", got)
	}
	// M1: the existing volume had read-only, now writable: asked again
	ro := ownersApp()
	ro.HostConfig.Mounts = append(ro.HostConfig.Mounts, mount.Mount{Type: mount.TypeVolume, Source: "v9", Target: "/h", ReadOnly: true})
	got, _ = editAsked(t, ro, func(c *types.ContainerJSON) {
		for i := range c.HostConfig.Mounts {
			if c.HostConfig.Mounts[i].Source == "v9" {
				c.HostConfig.Mounts[i].ReadOnly = false
			}
		}
	})
	if got != "radarr|bind|/etc" {
		t.Errorf("a disguised volume made writable: %q", got)
	}
	if got, _ := editAsked(t, ro, nil); got != "" {
		t.Errorf("kept read-only: %q", got)
	}
}

func TestNestedBindsNeedTheTick(t *testing.T) {
	review2Server(t, []writer{
		{app: "radarr", source: "/mnt/data/media", writable: true},
		{app: "reader", source: "/mnt/data/appB", writable: false},
	}, nil)
	before := ownersApp()
	add := func(source string) func(*types.ContainerJSON) {
		return func(c *types.ContainerJSON) {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: source, Target: "/new"})
		}
	}
	cases := map[string]string{
		"/mnt/data/media/movies":    "radarr|nested|/mnt/data/media/movies",    // inside another app's writable folder
		"/mnt/data/media":           "",                                        // the same folder: shared, not nested
		"/mnt/data/appB/secrets":    "",                                        // inside a folder only read
		"/mnt/data/radarr/config/x": "radarr|nested|/mnt/data/radarr/config/x", // inside its own writable bind
	}
	for source, want := range cases {
		got, err := editAsked(t, before, add(source))
		if err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", source, got, err, want)
		}
	}
	// the words name the app that could redirect it
	after := edited(before, add("/mnt/data/media/movies"))
	where, _ := editAccess(before, &after)
	if len(where) != 1 || !strings.Contains(where[0].Detail, "radarr") || !strings.Contains(sensitiveSentence(where), "could turn it into a link") {
		t.Errorf("words: %+v", where)
	}
	// two services of one request: one writes the folder, the other mounts below it
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"a": {Volumes: []mount.Mount{{Type: mount.TypeBind, Source: "/mnt/data/stack", Target: "/data"}}},
		"b": {Volumes: []mount.Mount{{Type: mount.TypeBind, Source: "/mnt/data/stack/sub", Target: "/sub", ReadOnly: true}}},
	}}
	if got, err := createAsked(t, request); err != nil || got != "b|nested|/mnt/data/stack/sub" {
		t.Errorf("two services: %q %v", got, err)
	}
	// binds the container had are not asked again
	nested := ownersApp()
	nested.HostConfig.Binds = append(nested.HostConfig.Binds, "/mnt/data/media/movies:/movies")
	if got, _ := editAsked(t, nested, nil); got != "" {
		t.Errorf("an existing nested bind: %q", got)
	}
}

func TestALinkIntoAnotherAppsFolderIsSaid(t *testing.T) {
	review2Server(t, []writer{{app: "appB", source: "/mnt/data/appB", writable: true}}, nil)
	before := ownersApp()
	after := edited(before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/appA/config/theirs", Target: "/t"})
	})
	where, err := editAccess(before, &after)
	if err != nil {
		t.Fatal(err)
	}
	got := keysOf(where)
	if !strings.Contains(got, "radarr|linkinto|/mnt/data/appB/secrets") || !strings.Contains(sensitiveSentence(where), "appB") {
		t.Fatalf("a link into appB's folder: %q", got)
	}
}

func TestADeviceKeepsItsPermissionsWhenItMoves(t *testing.T) {
	withServer(t)
	c := shapeContainer(t, true)
	c.HostConfig.Devices = []containerType.DeviceMapping{{PathOnHost: "/dev/dri/renderD128", PathInContainer: "/dev/dri/renderD128", CgroupPermissions: "r"}}
	fake := withFakeDocker(t, c)
	fake.lenient = true
	updateThroughRoute(t, `{"devices":["/dev/dri/renderD128:/dev/dri/card9"]}`)
	var got createdBody
	if len(fake.created) != 1 || json.Unmarshal(fake.created[0], &got) != nil {
		t.Fatalf("not created: %v", fake.others)
	}
	if d := got.HostConfig.Devices; len(d) != 1 || d[0].CgroupPermissions != "r" || d[0].PathInContainer != "/dev/dri/card9" {
		t.Fatalf("devices: %+v", d)
	}
}

func createdThrough(t *testing.T, c types.ContainerJSON, body string) createdBody {
	t.Helper()
	fake := withFakeDocker(t, c)
	fake.lenient = true
	w := updateThroughRoute(t, body)
	var got createdBody
	if len(fake.created) != 1 || json.Unmarshal(fake.created[0], &got) != nil {
		t.Fatalf("not created: %d %s", w.Code, w.Body.String())
	}
	return got
}

func TestTheKeepFollowsTheReadOnlyFlagAndTheAddress(t *testing.T) {
	withServer(t)
	c := shapeContainer(t, true)
	volumes := tabPayloads(c)["Storage"]["Volumes"].([]map[string]interface{})
	flip := func(target string) string {
		changed := []map[string]interface{}{}
		for _, v := range volumes {
			copied := map[string]interface{}{}
			for k, x := range v {
				copied[k] = x
			}
			if copied["Target"] == target {
				copied["ReadOnly"] = !copied["ReadOnly"].(bool)
			}
			changed = append(changed, copied)
		}
		b, _ := json.Marshal(map[string]interface{}{"Volumes": changed})
		return string(b)
	}
	// a bind string made read-only: still a bind string, now ro, its other options kept
	got := createdThrough(t, c, flip("/b"))
	if !strings.Contains(strings.Join(got.HostConfig.Binds, " "), "/b:ro,rslave") {
		t.Errorf("rw to ro: %v", got.HostConfig.Binds)
	}
	// a read-only bind string made writable
	got = createdThrough(t, c, flip("/a"))
	if !strings.Contains(strings.Join(got.HostConfig.Binds, " "), "/a:z") {
		t.Errorf("ro to rw: %v", got.HostConfig.Binds)
	}
	// a mount made read-only keeps its options
	got = createdThrough(t, c, flip("/c"))
	for _, m := range got.HostConfig.Mounts {
		if m.Target == "/c" && (!m.ReadOnly || m.BindOptions == nil || m.BindOptions.Propagation != mount.PropagationRSlave) {
			t.Errorf("mount made read-only: %+v", m)
		}
	}
	// a tab that sends "" for a stored 0.0.0.0, and 0.0.0.0 for a stored "": the stored one
	got = createdThrough(t, c, `{"portBindings":{"9001/udp":[{"HostIp":"","HostPort":"18301"}],"9000/tcp":[{"HostIp":"127.0.0.1","HostPort":"18300"}],"9003/tcp":[{"HostIp":"0.0.0.0","HostPort":""}]},"exposedPorts":["9000/tcp","9001/udp","9002/tcp","9003/tcp"]}`)
	if got.HostConfig.PortBindings["9001/udp"][0].HostIP != "0.0.0.0" || got.HostConfig.PortBindings["9003/tcp"][0].HostIP != "" {
		t.Errorf("host addresses: %+v", got.HostConfig.PortBindings)
	}
	// M7: a source written with a trailing slash is the same mount; options sent with it are used
	got = createdThrough(t, c, strings.Replace(flip("/none"), `"Target":"/c"`, `"Target":"/c/"`, 1))
	for _, m := range got.HostConfig.Mounts {
		if m.Target == "/c" && (m.BindOptions == nil || m.BindOptions.Propagation != mount.PropagationRSlave) {
			t.Errorf("a trailing slash lost the options: %+v", m)
		}
	}
	sent := []mount.Mount{}
	for _, m := range c.HostConfig.Mounts {
		if m.Target == "/c" {
			m.BindOptions = &mount.BindOptions{Propagation: mount.PropagationPrivate}
		}
		sent = append(sent, m)
	}
	b, _ := json.Marshal(map[string]interface{}{"Volumes": sent})
	got = createdThrough(t, c, string(b))
	for _, m := range got.HostConfig.Mounts {
		if m.Target == "/c" && (m.BindOptions == nil || m.BindOptions.Propagation != mount.PropagationPrivate) {
			t.Errorf("options sent with a mount were dropped: %+v", m)
		}
	}
}

func TestEditBounds(t *testing.T) {
	withServer(t)
	before := ownersApp()
	after := edited(before, func(c *types.ContainerJSON) {
		c.Config.ExposedPorts = nat.PortSet{}
		for i := 0; i < 1100; i++ {
			c.Config.ExposedPorts[nat.Port(strings.Repeat("1", 1)+string(rune('0'+i%10))+"/tcp")+nat.Port("")] = struct{}{}
		}
		for i := 0; i < 1100; i++ {
			c.Config.ExposedPorts[nat.Port("p"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+"/tcp")] = struct{}{}
		}
	})
	if err := checkEdit(before, after); err == nil || !strings.Contains(err.Error(), "ports") {
		t.Fatalf("1100 ports: %v", err)
	}
	// under userns-remap the new folder is made, not given away
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mnt/data"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPlaces, oldRemap := EditDataPlaces, UsernsState
	defer func() { EditDataPlaces, UsernsState = oldPlaces, oldRemap }()
	EditDataPlaces = func() []string { return []string{"/mnt/data"} }
	UsernsState = func() (bool, bool) { return true, true }
	owned := recordChowns(t, root)
	b := ownersApp()
	b.Config.User = ""
	forkPrepareBindFolders(b, edited(b, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/remapped", Target: "/r"})
	}), root)
	if _, err := os.Stat(filepath.Join(root, "mnt/data/remapped")); err != nil || len(owned) != 0 {
		t.Fatalf("userns-remap: %v %v", err, owned)
	}
}

func TestTheSecondReviewsSensitivePlacesAndOptions(t *testing.T) {
	for p, want := range map[string]string{"/home": "bind", "/home/me": "bind", "/home/me/music": "", "/var/spool/cron/crontabs": "bind", "/var/lib/libvirt/images": "bind", "/var/lib/kubelet": "bind"} {
		if got := SensitiveBind(p, nil); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
	items := serviceSensitive("a", ContainerCreateRequestContainer{NetworkMode: "container:gluetun",
		SecurityOpt: []string{"label=disable", "seccomp=/tmp/x.json", "no-new-privileges:false", "no-new-privileges:true", "seccomp=default"}}, nil)
	if got := keysOf(items); got != "a|network-container|gluetun a|unconfined|label=disable a|unconfined|seccomp=/tmp/x.json a|unconfined|no-new-privileges:false" {
		t.Fatalf("%q", got)
	}
}

func TestTheSensitiveRouteAnswersWhatTheCreationAsks(t *testing.T) {
	review2Server(t, []writer{{app: "radarr", source: "/mnt/data/media", writable: true}}, nil)
	serve := func(auth *utils.AuthContext, body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/cosmos/api/install/sensitive", strings.NewReader(body))
		if auth != nil {
			req = req.WithContext(context.WithValue(req.Context(), utils.AuthCtxKey, auth))
		}
		w := httptest.NewRecorder()
		SensitiveRoute(w, req)
		return w.Code, w.Body.String()
	}
	reader := &utils.AuthContext{Nickname: "bob", Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES_READ}}
	admin := &utils.AuthContext{Nickname: "alice", IsSudoed: true, Permissions: []utils.Permission{utils.PERM_LOGIN, utils.PERM_RESOURCES}}
	definition := `{"definition":{"services":{"jellyfin":{"image":"x","volumes":[{"Type":"bind","Source":"/mnt/data/media/movies","Target":"/movies","ReadOnly":true},{"Type":"bind","Source":"/etc","Target":"/e"}]}}}}`
	if code, _ := serve(nil, definition); code != http.StatusUnauthorized {
		t.Errorf("no session: %d", code)
	}
	if code, _ := serve(reader, definition); code == http.StatusOK {
		t.Errorf("a reader: %d", code)
	}
	code, body := serve(admin, definition)
	if code != http.StatusOK || !strings.Contains(body, `"jellyfin|`) && !strings.Contains(body, `"kind":"nested"`) || !strings.Contains(body, `"value":"/etc"`) {
		t.Fatalf("answer: %d %s", code, body)
	}
	if !strings.Contains(body, `"detail":"radarr (/mnt/data/media)"`) {
		t.Errorf("the nested folder's words: %s", body)
	}
	if code, body := serve(admin, `{"definition":{"services":{"a":{"volumes":[{"Type":"bind","Source":"/mnt/data/appA/config/srv","Target":"/s"}]}}}}`); code != http.StatusOK || !strings.Contains(body, "refused") {
		t.Errorf("a link out: %d %s", code, body)
	}
	if code, _ := serve(admin, "not json"); code != http.StatusBadRequest {
		t.Errorf("bad body: %d", code)
	}
}

// S-1 (third review): the creation route itself requires the ticks only the server sees.
func TestTheCreationRouteRequiresTheNestedTick(t *testing.T) {
	review2Server(t, []writer{{app: "radarr", source: "/mnt/data/media", writable: true}}, nil)
	request := func() DockerServiceCreateRequest {
		return DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
			"jellyfin": {Image: "x", Volumes: []mount.Mount{{Type: mount.TypeBind, Source: "/mnt/data/media/movies", Target: "/movies", ReadOnly: true}}},
		}}
	}
	recorder := httptest.NewRecorder()
	if forkRefuseService(recorder, httptest.NewRequest("POST", "/cosmos/api/docker-service", nil), request(), func() {}) {
		t.Fatal("created without the tick")
	}
	if body := recorder.Body.String(); !strings.Contains(body, "/mnt/data/media/movies") || !strings.Contains(body, "radarr") {
		t.Fatalf("answer: %s", body)
	}
	oldExists := containerExists
	defer func() { containerExists = oldExists }()
	containerExists = func(string) bool { return false }
	confirmed := httptest.NewRequest("POST", "/cosmos/api/docker-service?"+url.Values{"confirm": {"jellyfin|nested|/mnt/data/media/movies"}, "anyArch": {"1"}}.Encode(), nil)
	recorder = httptest.NewRecorder()
	if !forkRefuseService(recorder, confirmed, request(), func() {}) {
		t.Fatalf("refused although ticked: %s", recorder.Body.String())
	}
}

// m-4 (third review): when Docker cannot list the containers, a new folder on a
// data place asks the tick that says it could not be checked (fail closed).
func TestUncheckedWhenTheContainersCannotBeListed(t *testing.T) {
	review2Server(t, nil, nil)
	listServerMounts = func() ([]writer, bool) { return nil, false }
	before := ownersApp()
	got, err := editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/new", Target: "/n"})
	})
	if err != nil || got != "radarr|unchecked|/mnt/data/new" {
		t.Fatalf("edit: %q %v", got, err)
	}
	if got, _ := createAsked(t, DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"a": {Volumes: []mount.Mount{{Type: mount.TypeBind, Source: "/mnt/data/new", Target: "/n"}}}}}); got != "a|unchecked|/mnt/data/new" {
		t.Fatalf("creation: %q", got)
	}
	// a failure is not kept: once Docker answers, nothing more is asked
	listServerMounts = func() ([]writer, bool) { return nil, true }
	if got, _ := editAsked(t, before, func(c *types.ContainerJSON) {
		c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/new", Target: "/n"})
	}); got != "" {
		t.Fatalf("after Docker answered: %q", got)
	}
}

// m-4: the cache of volumes drops its oldest entry, not all of them; m-3: a name Docker refuses is never asked.
func TestVolumeLookupsAreBoundedAndCheckedFirst(t *testing.T) {
	review2Server(t, nil, nil)
	asked := 0
	lookupVolume = func(name string) (string, map[string]string, bool) { asked++; return "local", nil, true }
	for i := 0; i < 300; i++ {
		existingVolumeDevice("v" + strconv.Itoa(i))
	}
	volumeFactsMu.Lock()
	size := len(volumeFacts)
	_, newest := volumeFacts["v299"]
	volumeFactsMu.Unlock()
	if size != 256 || !newest {
		t.Fatalf("cache: %d entries, newest kept %v", size, newest)
	}
	asked = 0
	for _, bad := range []string{"../../containers/json", "a/b", "", ".x", strings.Repeat("a", 300)} {
		existingVolumeDevice(bad)
	}
	if asked != 0 {
		t.Fatalf("Docker was asked for %d names it refuses", asked)
	}
	// the creation's shapes come before any look: a bad volume name is refused, nothing asked
	if err := CheckServiceRequest(DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"a": {Volumes: []mount.Mount{{Type: mount.TypeVolume, Source: "../x", Target: "/x"}}}}}); err == nil {
		t.Fatal("a bad volume name accepted")
	}
	many := []mount.Mount{}
	for i := 0; i < 300; i++ {
		many = append(many, mount.Mount{Type: mount.TypeTmpfs, Target: "/t" + strconv.Itoa(i)})
	}
	if err := CheckServiceRequest(DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"a": {Volumes: many}}}); err == nil {
		t.Fatal("300 mounts accepted")
	}
}

// m-3: the edit's shapes and bounds are checked before any lookup.
func TestTheEditChecksShapesBeforeLooking(t *testing.T) {
	review2Server(t, nil, nil)
	looked := 0
	resolveSource = func(p string) (string, bool, error) { looked++; return p, false, nil }
	before := ownersApp()
	after := edited(before, func(c *types.ContainerJSON) {
		for i := 0; i < 300; i++ {
			c.HostConfig.Mounts = append(c.HostConfig.Mounts, mount.Mount{Type: mount.TypeBind, Source: "/mnt/data/x" + strconv.Itoa(i), Target: "/x" + strconv.Itoa(i)})
		}
	})
	if _, refused := forkRefuseBadEdit(httptest.NewRecorder(), before, &after); !refused || looked != 0 {
		t.Fatalf("refused %v after %d lookups", refused, looked)
	}
}

// m-5: a failed answer about userns-remap is not remembered: unknown means no chown.
func TestUsernsUnknownIsNotRemembered(t *testing.T) {
	usernsMu.Lock()
	usernsKnown, usernsRemapped = false, false
	usernsMu.Unlock()
	old := DockerClient
	DockerClient = nil
	if remapped, known := UsernsState(); remapped || known || !UsernsRemapped() {
		t.Fatalf("no Docker: %v %v", remapped, known)
	}
	DockerClient = old
	usernsMu.Lock()
	known := usernsKnown
	usernsMu.Unlock()
	if known {
		t.Fatal("a failure was remembered")
	}
}
