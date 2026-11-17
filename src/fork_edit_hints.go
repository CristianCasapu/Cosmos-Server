package main

// Fork addition (plan 47, I2c): what the edit tabs of a container propose
// from, in one bounded read.
//
//   GET /cosmos/api/servapps/{containerId}/edit-hints
//
// answers what the local image declares (user, volumes, ports, variable
// names), the image's recipe and known app, the user the container writes
// as, the facts of its bind folders under the data places, the data places
// themselves (the install plan's), the server's memory, CPUs, home-network
// addresses, DNS resolvers and router, the devices a container may be given,
// and a free host port per container port. Nothing is changed. PERM_RESOURCES,
// like the edit itself. The detections are in src/edithints; the client's
// rules in client/src/utils/editHints.js.

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/behind"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/edithints"
	"github.com/azukaar/cosmos-server/src/proxy"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
	"github.com/gorilla/mux"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	editHintsMaxList    = 64
	editHintsMaxFolders = 32
	editHintsMaxPorts   = 16
)

type editHintsImage struct {
	User    string   `json:"user"`
	Volumes []string `json:"volumes"`
	Ports   []string `json:"ports"`
	Env     []string `json:"env"`
}

type editHintsPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	// Role: web (its web page), service (what it serves), other
	Role string `json:"role"`
	Why  string `json:"why,omitempty"`
}

type editHintsRecipe struct {
	Name        string          `json:"name"`
	Source      string          `json:"source"`
	Ports       []editHintsPort `json:"ports"`
	User        string          `json:"user,omitempty"`
	Env         []string        `json:"env"`
	Volumes     []string        `json:"volumes"`
	Memory      string          `json:"memory,omitempty"`
	MemoryLimit int64           `json:"memoryLimit,omitempty"`
}

type editHintsKnown struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type editHintsFolder struct {
	edithints.Folder
	Target   string `json:"target"`
	ReadOnly bool   `json:"readOnly"`
}

type editHintsDNS struct {
	Servers []string `json:"servers"`
	// Source: resolv.conf, systemd-resolved, "" (none usable: Docker uses public servers)
	Source string `json:"source"`
	Router string `json:"router"`
}

type editHintsServer struct {
	Memory uint64 `json:"memory"`
	CPUs   int    `json:"cpus"`
}

type editHints struct {
	Container   string            `json:"container"`
	Image       editHintsImage    `json:"image"`
	Recipe      *editHintsRecipe  `json:"recipe,omitempty"`
	KnownApp    *editHintsKnown   `json:"knownApp,omitempty"`
	Linuxserver bool              `json:"linuxserver"`
	RunsAs      edithints.RunAs   `json:"runsAs"`
	Folders     []editHintsFolder `json:"folders"`
	// Anonymous: folders of the container kept in a volume Docker made by itself (lost when it is made again)
	Anonymous []string           `json:"anonymous"`
	DataPath  string             `json:"dataPath"`
	DataOwner string             `json:"dataOwner"`
	Places    []installPlace     `json:"places"`
	Server    editHintsServer    `json:"server"`
	LAN       []proxy.LANAddress `json:"lan"`
	DNS       editHintsDNS       `json:"dns"`
	Devices   []edithints.Device `json:"devices"`
	FreePorts map[string]int     `json:"freePorts"`
	// PortsListened: the free ports were also checked against the server's sockets (not when Cosmos runs in a container)
	PortsListened bool `json:"portsListened"`
	// UsernsRemap: Docker runs apps in a user namespace of its own; a new data folder stays root's
	UsernsRemap bool `json:"usernsRemap"`
	// VPN: the server's addresses on VPN interfaces (a port published there works only while the VPN is up)
	VPN []editHintsVPN `json:"vpn"`
}

type editHintsVPN struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
}

// what the hints are made of, gathered by editHintsFacts; the tests give their own
type editHintsInput struct {
	Container   types.ContainerJSON
	ImageUser   string
	ImageVols   []string
	ImagePorts  []string
	ImageEnv    []string
	Plan        installPlanAnswer
	Server      editHintsServer
	LAN         []proxy.LANAddress
	DNS         editHintsDNS
	Devices     []edithints.Device
	Held        map[string]bool // "8080/tcp" published by another container
	CosmosPort  []int
	Listen      edithints.Taken // nil when the server's sockets cannot be read
	VPN         []editHintsVPN
	UsernsRemap bool
	// Inspect gives the facts of one bind's folder (bounded in time by the route); edithints.InspectFolder when nil
	Inspect func(source string, places []string, user edithints.RunAs) edithints.Folder
	Root    string // where the server's files are seen
}

var anonymousVolume = regexp.MustCompile(`^[0-9a-f]{64}$`)

func boundedNames(list []string, n int) []string {
	out := []string{}
	for _, item := range list {
		if len(out) >= n {
			break
		}
		if clean := strings.TrimSpace(item); clean != "" && len(clean) <= 255 {
			out = append(out, clean)
		}
	}
	return out
}

func envNames(env []string) []string {
	names := []string{}
	for _, line := range env {
		name, _, _ := strings.Cut(line, "=")
		names = append(names, name)
	}
	return boundedNames(names, editHintsMaxList)
}

// parsePortText reads "8080", "8080/udp": the port and its protocol.
func parsePortText(text string) (int, string, bool) {
	number, protocol, found := strings.Cut(strings.TrimSpace(text), "/")
	if !found || protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return 0, "", false
	}
	port, err := strconv.Atoi(number)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", false
	}
	return port, protocol, true
}

// editDataPlaces are the places a bind folder is looked at in: the data path and the data disks.
func editDataPlaces(plan installPlanAnswer) []string {
	out := []string{}
	for _, place := range plan.Places {
		if place.Kind == "data-path" || place.Kind == "disk" {
			out = append(out, place.Path)
		}
	}
	return out
}

func buildEditHints(in editHintsInput) editHints {
	c := in.Container
	hints := editHints{
		Container: strings.TrimPrefix(c.Name, "/"),
		Image: editHintsImage{
			User:    in.ImageUser,
			Volumes: boundedNames(in.ImageVols, editHintsMaxList),
			Ports:   boundedNames(in.ImagePorts, editHintsMaxList),
			Env:     envNames(in.ImageEnv),
		},
		Folders:     []editHintsFolder{},
		Anonymous:   []string{},
		DataPath:    in.Plan.DataPath,
		DataOwner:   in.Plan.DataOwner,
		Places:      in.Plan.Places,
		Server:      in.Server,
		LAN:         in.LAN,
		DNS:         in.DNS,
		Devices:     in.Devices,
		FreePorts:   map[string]int{},
		VPN:         in.VPN,
		UsernsRemap: in.UsernsRemap,
	}
	if hints.VPN == nil {
		hints.VPN = []editHintsVPN{}
	}
	if hints.LAN == nil {
		hints.LAN = []proxy.LANAddress{}
	}
	if hints.Devices == nil {
		hints.Devices = []edithints.Device{}
	}
	if hints.Places == nil {
		hints.Places = []installPlace{}
	}
	image := ""
	if c.Config != nil {
		image = c.Config.Image
	}

	wanted := []string{}
	want := func(port int, protocol string) {
		key := strconv.Itoa(port) + "/" + protocol
		for _, w := range wanted {
			if w == key {
				return
			}
		}
		if len(wanted) < editHintsMaxPorts {
			wanted = append(wanted, key)
		}
	}
	recipeEnv := []string{}
	if recipe := anyimage.RecipeForImage(image); recipe != nil {
		r := &editHintsRecipe{Name: recipe.Name, Source: recipe.Source, Ports: []editHintsPort{}, User: recipe.User, Env: []string{}, Volumes: []string{}, Memory: recipe.Memory}
		r.MemoryLimit = edithints.MemoryFromHint(recipe.Memory)
		add := func(p *anyimage.RecipePort, role string) {
			if p != nil && p.Port > 0 && len(r.Ports) < editHintsMaxList {
				r.Ports = append(r.Ports, editHintsPort{Port: p.Port, Protocol: "tcp", Role: role, Why: p.Why})
			}
		}
		add(recipe.Web, "web")
		add(recipe.Service, "service")
		for i := range recipe.Ports {
			add(&recipe.Ports[i], "other")
		}
		for _, e := range recipe.Env {
			recipeEnv = append(recipeEnv, e.Name)
		}
		r.Env = boundedNames(recipeEnv, editHintsMaxList)
		for _, v := range recipe.Volumes {
			r.Volumes = append(r.Volumes, v.Target)
		}
		r.Volumes = boundedNames(r.Volumes, editHintsMaxList)
		hints.Recipe = r
		for _, p := range r.Ports {
			want(p.Port, p.Protocol)
		}
	}
	if app := behind.AppFor(image); app != nil {
		hints.KnownApp = &editHintsKnown{Name: app.Name, Port: app.Port}
		if app.Port > 0 {
			want(app.Port, "tcp")
		}
	}
	hints.Linuxserver = edithints.LinuxserverStyle(image, recipeEnv)
	hints.RunsAs = docker.ContainerRunsAs(c, in.ImageUser) // the update route's own rule

	sortedPorts := append([]string{}, hints.Image.Ports...)
	if c.Config != nil {
		for port := range c.Config.ExposedPorts {
			sortedPorts = append(sortedPorts, string(port))
		}
	}
	sort.Strings(sortedPorts)
	for _, text := range sortedPorts {
		if port, protocol, ok := parsePortText(text); ok {
			want(port, protocol)
		}
	}
	cosmos := map[int]bool{}
	for _, p := range in.CosmosPort {
		cosmos[p] = true
	}
	taken := func(port int, protocol string) bool {
		if in.Held[strconv.Itoa(port)+"/"+protocol] || protocol == "tcp" && cosmos[port] {
			return true
		}
		return in.Listen != nil && in.Listen(port, protocol)
	}
	for _, key := range wanted {
		port, protocol, _ := parsePortText(key)
		if proposed := edithints.ProposeHostPort(port, protocol, taken); proposed > 0 {
			hints.FreePorts[key] = proposed
		}
	}
	hints.PortsListened = in.Listen != nil

	places := editDataPlaces(in.Plan)
	for _, m := range c.Mounts {
		if string(m.Type) == "volume" && anonymousVolume.MatchString(m.Name) && len(hints.Anonymous) < editHintsMaxList {
			hints.Anonymous = append(hints.Anonymous, m.Destination)
		}
		if string(m.Type) != "bind" || len(hints.Folders) >= editHintsMaxFolders {
			continue
		}
		inspect := in.Inspect
		if inspect == nil {
			inspect = func(source string, places []string, user edithints.RunAs) edithints.Folder {
				return edithints.InspectFolder(in.Root, source, places, user)
			}
		}
		folder := inspect(m.Source, places, hints.RunsAs)
		hints.Folders = append(hints.Folders, editHintsFolder{Folder: folder, Target: m.Destination, ReadOnly: !m.RW})
	}
	return hints
}

// ---- bounds of the route ----

// editHintsMaxTime bounds the reads of the disks (statfs, lstat): a network
// disk that does not answer must not hold the route.
var editHintsMaxTime = 3 * time.Second

var (
	abandonedMu sync.Mutex
	// abandoned: the keys (a data place, "places") whose read is still blocked in the kernel after
	// the route gave up on it; at most one such read per key, none started again until it ends
	abandoned = map[string]bool{}
)

// boundedOn runs fn in a goroutine and waits at most wait for its result;
// ok is false when it gave up (or a read on the same key is still blocked
// from an earlier request, in which case fn is not run at all). The result
// goes through a channel: a read given up on writes nothing the route still
// holds.
func boundedOn[T any](key string, wait time.Duration, fn func() T) (T, bool) {
	var zero T
	abandonedMu.Lock()
	if abandoned[key] || wait <= 0 {
		abandonedMu.Unlock()
		return zero, false
	}
	abandonedMu.Unlock()
	results := make(chan T, 1)
	var mu sync.Mutex
	gaveUp := false
	go func() {
		value := fn()
		mu.Lock()
		defer mu.Unlock()
		if gaveUp {
			abandonedMu.Lock()
			delete(abandoned, key)
			abandonedMu.Unlock()
		}
		// sent while the lock is held: the waiter either finds it or has already given up (second review M3)
		if hook, ok := boundedOnBeforeSend.Load().(func()); ok && hook != nil {
			hook()
		}
		results <- value
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case value := <-results:
		return value, true
	case <-timer.C:
		mu.Lock()
		defer mu.Unlock()
		select {
		case value := <-results:
			return value, true
		default:
		}
		gaveUp = true
		abandonedMu.Lock()
		abandoned[key] = true
		abandonedMu.Unlock()
		return zero, false
	}
}

// boundedOnBeforeSend holds a func() run just before a result is handed over; the tests make it slow.
var boundedOnBeforeSend atomic.Value

// editHintsRate: at most 2 requests at a time and 60 a minute (each opening of a tab asks once)
type hintsRate struct {
	mu      sync.Mutex
	running int
	window  time.Time
	count   int
}

var editHintsRate = &hintsRate{}

func (r *hintsRate) start(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.window) > time.Minute {
		r.window, r.count = now, 0
	}
	if r.running >= 2 || r.count >= 60 {
		return false
	}
	r.running++
	r.count++
	return true
}

func (r *hintsRate) done() {
	r.mu.Lock()
	r.running--
	r.mu.Unlock()
}

// ---- what the server tells ----

var vpnInterface = regexp.MustCompile(`^(tun|tap|wg|tailscale|zt|nebula|ppp|utun|ipsec|vpn)`)

// editHintsVPNAddresses lists the server's addresses on VPN interfaces.
var editHintsVPNAddresses = func() []editHintsVPN {
	out := []editHintsVPN{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range interfaces {
		if !vpnInterface.MatchString(iface.Name) {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, a := range addresses {
			if n, ok := a.(*net.IPNet); ok && len(out) < 16 {
				out = append(out, editHintsVPN{Interface: iface.Name, Address: n.IP.String()})
			}
		}
	}
	return out
}

var editHintsDevices = func() []edithints.Device {
	return edithints.Devices(edithints.Roots{Dev: hostFile("/dev"), Sys: "/sys"})
}

var editHintsDNSFacts = func() editHintsDNS {
	servers, source := edithints.Resolvers(hostFile("/etc/resolv.conf"), hostFile("/run/systemd/resolve/resolv.conf"))
	return editHintsDNS{Servers: servers, Source: source, Router: edithints.Gateway("/proc/net/route")}
}

var editHintsServerFacts = func() editHintsServer {
	server := editHintsServer{CPUs: runtime.NumCPU()}
	if vm, err := mem.VirtualMemory(); err == nil {
		server.Memory = vm.Total
	}
	return server
}

// editHintsHeld lists the host ports other containers publish.
var editHintsHeld = func(skip string) map[string]bool {
	held := map[string]bool{}
	containers, err := docker.DockerClient.ContainerList(docker.DockerContext, conttype.ListOptions{All: true})
	if err != nil {
		return held
	}
	for _, c := range containers {
		mine := false
		for _, name := range c.Names {
			if strings.TrimPrefix(name, "/") == skip {
				mine = true
			}
		}
		if mine {
			continue
		}
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				held[strconv.Itoa(int(p.PublicPort))+"/"+strings.ToLower(p.Type)] = true
			}
		}
	}
	return held
}

func cosmosPorts(config utils.Config) []int {
	out := []int{}
	for _, text := range []string{config.HTTPConfig.HTTPPort, config.HTTPConfig.HTTPSPort} {
		if port, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			out = append(out, port)
		}
	}
	return out
}

func init() {
	// the update route makes a new data folder for the app's user only under these
	docker.EditDataPlaces = func() []string {
		places := dataDisks()
		if path := utils.GetMainConfig().DockerConfig.DefaultDataPath; path != "" {
			places = append(places, path)
		}
		return places
	}
}

// EditHintsRoute answers GET /cosmos/api/servapps/{containerId}/edit-hints.
func EditHintsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != http.MethodGet {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	if !editHintsRate.start(time.Now()) {
		utils.HTTPError(w, "Too many requests for the edit tabs' proposals: try again in a moment", http.StatusTooManyRequests, "EH004")
		return
	}
	defer editHintsRate.done()
	if err := docker.Connect(); err != nil {
		utils.HTTPError(w, "Docker is not reachable", http.StatusInternalServerError, "DS002")
		return
	}
	name := mux.Vars(req)["containerId"]
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\x00\r\n") {
		utils.HTTPError(w, "Invalid container name", http.StatusBadRequest, "EH001")
		return
	}
	container, err := docker.DockerClient.ContainerInspect(docker.DockerContext, name)
	if err != nil {
		utils.HTTPError(w, "No such container", http.StatusNotFound, "EH002")
		return
	}
	// ?folder=<path> (at most 8): only these folders' facts, for a row the form added
	if asked := req.URL.Query()["folder"]; len(asked) > 0 {
		if len(asked) > 8 {
			utils.HTTPError(w, "At most 8 folders", http.StatusBadRequest, "EH003")
			return
		}
		places := docker.EditDataPlaces()
		user := docker.ContainerRunsAs(container, "")
		deadline := time.Now().Add(editHintsMaxTime)
		folders := []edithints.Folder{}
		for _, source := range asked {
			if len(source) > 4096 {
				utils.HTTPError(w, "Invalid folder", http.StatusBadRequest, "EH003")
				return
			}
			folder := edithints.Folder{Source: source, State: "slow"}
			key := "outside"
			for _, p := range places {
				if clean := filepath.Clean(p); clean != "/" && (source == clean || strings.HasPrefix(source, clean+"/")) && len(clean) > len(key) {
					key = clean
				}
			}
			root := ""
			if utils.IsInsideContainer {
				root = "/mnt/host"
			}
			if got, ok := boundedOn(key, time.Until(deadline), func() edithints.Folder { return edithints.InspectFolder(root, source, places, user) }); ok {
				folder = got
			}
			folders = append(folders, folder)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]interface{}{"folders": folders}})
		return
	}

	config := utils.GetMainConfig()
	// the data places' free space is read from each disk: bounded, as the folders below
	plan, ok := boundedOn("places", editHintsMaxTime, func() installPlanAnswer { return buildInstallPlan(nil, config) })
	if !ok {
		plan = installPlanAnswer{Places: []installPlace{}}
	}
	in := editHintsInput{
		Container:  container,
		Plan:       plan,
		Server:     editHintsServerFacts(),
		LAN:        proxy.LANAddresses(),
		DNS:        editHintsDNSFacts(),
		Devices:    editHintsDevices(),
		Held:       editHintsHeld(strings.TrimPrefix(container.Name, "/")),
		CosmosPort: cosmosPorts(config),
	}
	if utils.IsInsideContainer {
		in.Root = "/mnt/host"
	} else {
		listening := edithints.ListeningCached() // the kernel's socket tables: nothing is bound
		in.Listen = func(port int, protocol string) bool { return listening[strconv.Itoa(port)+"/"+protocol] }
	}
	in.VPN = editHintsVPNAddresses()
	in.UsernsRemap = docker.UsernsRemapped()
	deadline := time.Now().Add(editHintsMaxTime)
	in.Inspect = func(source string, places []string, user edithints.RunAs) edithints.Folder {
		folder := edithints.Folder{Source: source, State: "slow"}
		key := "outside"
		for _, p := range places {
			if clean := filepath.Clean(p); clean != "/" && (source == clean || strings.HasPrefix(source, clean+"/")) && len(clean) > len(key) {
				key = clean
			}
		}
		if got, ok := boundedOn(key, time.Until(deadline), func() edithints.Folder { return edithints.InspectFolder(in.Root, source, places, user) }); ok {
			return got
		}
		return folder
	}
	if image, _, err := docker.DockerClient.ImageInspectWithRaw(docker.DockerContext, container.Image); err == nil && image.Config != nil {
		in.ImageUser, in.ImageEnv = image.Config.User, image.Config.Env
		for v := range image.Config.Volumes {
			in.ImageVols = append(in.ImageVols, filepath.Clean(v))
		}
		for p := range image.Config.ExposedPorts {
			in.ImagePorts = append(in.ImagePorts, string(p))
		}
		sort.Strings(in.ImageVols)
		sort.Strings(in.ImagePorts)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": buildEditHints(in)})
}
