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
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

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
	// PortsListened: the free ports were also tried on the server (not when Cosmos runs in a container)
	PortsListened bool `json:"portsListened"`
}

// what the hints are made of, gathered by editHintsFacts; the tests give their own
type editHintsInput struct {
	Container  types.ContainerJSON
	ImageUser  string
	ImageVols  []string
	ImagePorts []string
	ImageEnv   []string
	Plan       installPlanAnswer
	Server     editHintsServer
	LAN        []proxy.LANAddress
	DNS        editHintsDNS
	Devices    []edithints.Device
	Held       map[string]bool // "8080/tcp" published by another container
	CosmosPort []int
	Listen     edithints.Taken // nil when the server cannot be tried
	Asked      []string        // more container ports to propose a host port for
	Root       string          // where the server's files are seen
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
		Folders:   []editHintsFolder{},
		Anonymous: []string{},
		DataPath:  in.Plan.DataPath,
		DataOwner: in.Plan.DataOwner,
		Places:    in.Plan.Places,
		Server:    in.Server,
		LAN:       in.LAN,
		DNS:       in.DNS,
		Devices:   in.Devices,
		FreePorts: map[string]int{},
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
	image, user, env := "", "", []string{}
	if c.Config != nil {
		image, user, env = c.Config.Image, c.Config.User, c.Config.Env
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
	hints.RunsAs = edithints.EffectiveUser(user, in.ImageUser, env, hints.Linuxserver)

	sortedPorts := append([]string{}, hints.Image.Ports...)
	if c.Config != nil {
		for port := range c.Config.ExposedPorts {
			sortedPorts = append(sortedPorts, string(port))
		}
	}
	sort.Strings(sortedPorts)
	for _, text := range append(sortedPorts, in.Asked...) {
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
		folder := edithints.InspectFolder(in.Root, m.Source, places, hints.RunsAs)
		hints.Folders = append(hints.Folders, editHintsFolder{Folder: folder, Target: m.Destination, ReadOnly: !m.RW})
	}
	return hints
}

// ---- what the server tells ----

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
	config := utils.GetMainConfig()
	in := editHintsInput{
		Container:  container,
		Plan:       buildInstallPlan(nil, config),
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
		in.Listen = edithints.ListenTaken
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
