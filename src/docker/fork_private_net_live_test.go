package docker

// Fork addition (plan 45, review F2): a database and its web manager, created
// through the real creation path as "Install any image" sends them
// (client/src/utils/anyImage.js buildDefinition / managerDefinition: network
// mode and networks of their own), are never on Docker's default bridge.
// Busybox web servers stand in for the database and the manager.
//
//	COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/docker/ -run PrivateNetLive -v

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
)

func liveExec(t *testing.T, name string, cmd ...string) string {
	t.Helper()
	made, err := DockerClient.ContainerExecCreate(DockerContext, livePrefix+name, types.ExecConfig{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	attached, err := DockerClient.ContainerExecAttach(DockerContext, made.ID, types.ExecStartCheck{})
	if err != nil {
		t.Fatal(err)
	}
	defer attached.Close()
	var out bytes.Buffer
	io.Copy(&out, attached.Reader)
	return out.String()
}

func liveNetworkCleanup() {
	nets, _ := DockerClient.NetworkList(DockerContext, types.NetworkListOptions{})
	for _, n := range nets {
		if strings.Contains(n.Name, livePrefix) {
			DockerClient.NetworkRemove(DockerContext, n.ID)
		}
	}
}

func web(name string, port string, mode string, nets []string, links []string, labels map[string]string) ContainerCreateRequestContainer {
	s := ContainerCreateRequestContainer{
		Name: livePrefix + name, Image: liveImage, Command: forkStringList{list: []string{"httpd", "-f", "-p", port, "-h", "/etc"}, given: true},
		RestartPolicy: "no", NetworkMode: mode, Links: links, Labels: labels,
		Networks: map[string]ContainerCreateRequestServiceNetwork{},
	}
	for _, n := range nets {
		s.Networks[n] = ContainerCreateRequestServiceNetwork{}
	}
	return s
}

func TestPrivateNetLiveADatabaseAndItsManagerAreNotOnTheDefaultBridge(t *testing.T) {
	liveSetup(t)
	liveNetworkCleanup()
	t.Cleanup(func() { liveCleanup(t); liveNetworkCleanup() })

	// someone else's container on the default bridge, and a database made before (on it, as upstream makes them)
	liveContainer(t, "stranger", "", true)
	old := web("olddb", "8090", "bridge", nil, nil, nil)
	if err := CreateService(DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"olddb": old}}, func(string) {}); err != nil {
		t.Fatal(err)
	}

	// a new database with its manager: one network made for them, the network mode of both
	net := livePrefix + "db-manager-k3x9ab"
	request := DockerServiceCreateRequest{
		Services: map[string]ContainerCreateRequestContainer{
			"db":      web("db", "8080", net, []string{net}, nil, nil),
			"manager": web("manager", "8081", net, []string{net}, nil, map[string]string{"cosmos-fork-manager-of": livePrefix + "db"}),
		},
		Networks: map[string]ContainerCreateRequestNetwork{net: {Driver: "bridge"}},
	}
	// and a manager added to the existing database: a network of its own, and the link network with it
	own := livePrefix + "olddb-manager-q7w8e9"
	request.Services["oldmanager"] = web("oldmanager", "8082", own, []string{own}, []string{livePrefix + "olddb"}, map[string]string{"cosmos-fork-manager-of": livePrefix + "olddb"})
	request.Networks[own] = ContainerCreateRequestNetwork{Driver: "bridge"}
	log := []string{}
	if err := CreateService(request, func(line string) { log = append(log, line) }); err != nil {
		t.Fatalf("%v\n%s", err, strings.Join(log, ""))
	}
	time.Sleep(2 * time.Second)

	ipOn := func(name string) map[string]string {
		c := liveInspect(t, name)
		out := map[string]string{}
		for n, e := range c.NetworkSettings.Networks {
			out[n] = e.IPAddress
		}
		return out
	}
	for _, name := range []string{"db", "manager", "oldmanager"} {
		nets := ipOn(name)
		if _, on := nets["bridge"]; on {
			t.Errorf("%s is on the default bridge: %v", name, nets)
		}
		if len(nets) == 0 {
			t.Errorf("%s is on no network", name)
		}
	}
	if nets := ipOn("oldmanager"); len(nets) != 2 {
		t.Errorf("the added manager: its own network and the link network only: %v", nets)
	}

	reach := func(from string, address string) bool {
		out := liveExec(t, from, "wget", "-q", "-T", "3", "-O", "-", "http://"+address+"/hostname")
		return strings.TrimSpace(out) != "" && !strings.Contains(out, "wget:")
	}
	// the manager reaches its database by name; the added one reaches the old database
	if !reach("manager", livePrefix+"db:8080") {
		t.Error("the manager does not reach its database")
	}
	if !reach("oldmanager", livePrefix+"olddb:8090") {
		t.Error("the added manager does not reach the existing database")
	}
	// a stranger on the default bridge reaches neither, at any of their addresses
	for _, name := range []string{"db", "manager", "oldmanager"} {
		port := map[string]string{"db": "8080", "manager": "8081", "oldmanager": "8082"}[name]
		for n, ip := range ipOn(name) {
			if ip != "" && reach("stranger", ip+":"+port) {
				t.Errorf("a container on the default bridge reaches %s at %s (%s)", name, ip, n)
			}
		}
	}
	// Cosmos still finds an address for the manager's route
	for _, name := range []string{"manager", "oldmanager"} {
		ip, state, err := ContainerTarget(livePrefix + name)
		if err != nil || ip == "" || state != TargetRunning {
			t.Errorf("no address for the route of %s: %q %v %v", name, ip, state, err)
		}
	}
}
