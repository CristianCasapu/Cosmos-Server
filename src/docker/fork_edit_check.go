package docker

// Fork addition (plan 47, I2c): what the edit tabs of a container send is
// checked by the server too, before Docker is asked. Only what changes is
// checked: a container that saved before with odd but valid settings (a
// port on every address, a folder outside the data disks, a device by an old
// name) saves again unchanged.
//
//   - a published port's host address must be one of this server's, a
//     loopback or the unspecified one (0.0.0.0, ::), its host port a number
//     or a range, its protocol tcp, udp or sctp;
//   - a bind's source must be an absolute path without "..", its target too;
//     a named volume follows Docker's name rule;
//   - a device must be a path under /dev that exists, given to an absolute
//     path in the container;
//   - the user, the network mode, the restart policy and the CPUs follow the
//     creation's rules (fork_service_check.go); DNS servers are IP addresses.
//
// A new bind folder under a data place is created owned by the container's
// user (forkPrepareBindFolders), so that the app can write in it.

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/azukaar/cosmos-server/src/edithints"
	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"golang.org/x/sys/unix"
)

const (
	maxEditMounts  = 256
	maxEditDevices = 64
	maxEditDNS     = 8
	maxEditPorts   = 1024
)

// volumeNamePattern is Docker's rule for a volume's name.
var volumeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// serverAddresses are this server's own addresses, nil when they cannot be
// known (Cosmos in a container sees its own). Replaced by the tests.
var serverAddresses = func() []net.IP {
	if utils.IsInsideContainer {
		return nil
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := []net.IP{}
	for _, address := range addresses {
		if network, ok := address.(*net.IPNet); ok {
			out = append(out, network.IP)
		}
	}
	return out
}

// deviceExists says whether a device path exists on the server; true when
// that cannot be known. Replaced by the tests.
var deviceExists = func(p string) bool {
	if utils.IsInsideContainer {
		if _, err := os.Stat("/mnt/host"); err != nil {
			return true
		}
		p = "/mnt/host" + p
	}
	_, err := os.Stat(p)
	return err == nil
}

// cleanAbsolute is the clean form of an absolute path without ".." and
// control characters, "" for anything else.
func cleanAbsolute(p string) string { return edithints.CleanSource(p) }

func hostAddressProblem(text string, own []net.IP) string {
	text = strings.Trim(strings.TrimSpace(text), "[]")
	if text == "" {
		return ""
	}
	ip := net.ParseIP(text)
	if ip == nil {
		return fmt.Sprintf("the host address %q is not an IP address", text)
	}
	if ip.IsUnspecified() || ip.IsLoopback() || own == nil {
		return ""
	}
	for _, mine := range own {
		if mine.Equal(ip) {
			return ""
		}
	}
	return fmt.Sprintf("the host address %s is not an address of this server: Docker can only publish a port on one of the server's own addresses, 127.0.0.1, or every address (leave it empty)", ip.String())
}

func bindingKey(port nat.Port, binding nat.PortBinding) string {
	return string(port) + "|" + strings.Trim(binding.HostIP, "[]") + "|" + binding.HostPort
}

func checkPorts(before, after types.ContainerJSON) error {
	had := map[string]bool{}
	if before.HostConfig != nil {
		for port, bindings := range before.HostConfig.PortBindings {
			for _, binding := range bindings {
				had[bindingKey(port, binding)] = true
			}
		}
	}
	count := 0
	own := serverAddresses()
	for port, bindings := range after.HostConfig.PortBindings {
		for _, binding := range bindings {
			count++
			if count > maxEditPorts {
				return fmt.Errorf("more than %d published ports", maxEditPorts)
			}
			if had[bindingKey(port, binding)] {
				continue
			}
			proto, number := port.Proto(), port.Port()
			if proto != "tcp" && proto != "udp" && proto != "sctp" {
				return fmt.Errorf("port %s: the protocol must be TCP or UDP", port)
			}
			if _, _, err := portRange(number); err != nil {
				return fmt.Errorf("port %s of the container: %s", number, err.Error())
			}
			if binding.HostPort != "" {
				if _, _, err := portRange(binding.HostPort); err != nil {
					return fmt.Errorf("host port %s: %s", binding.HostPort, err.Error())
				}
			}
			if problem := hostAddressProblem(binding.HostIP, own); problem != "" {
				return fmt.Errorf("port %s: %s", number, problem)
			}
		}
	}
	return nil
}

func mountKey(m mount.Mount) string {
	return string(m.Type) + "|" + m.Source + "|" + m.Target
}

// mountsOf lists a container's mounts, its binds read as mounts.
func mountsOf(c types.ContainerJSON) []mount.Mount {
	if c.HostConfig == nil {
		return nil
	}
	list := append([]mount.Mount{}, c.HostConfig.Mounts...)
	for _, bind := range c.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) < 2 {
			continue
		}
		kind := mount.TypeVolume
		if strings.HasPrefix(parts[0], "/") {
			kind = mount.TypeBind
		}
		list = append(list, mount.Mount{Type: kind, Source: parts[0], Target: parts[1], ReadOnly: len(parts) > 2 && hasWord(parts[2], "ro")})
	}
	return list
}

func checkMounts(before, after types.ContainerJSON) error {
	had := map[string]bool{}
	for _, m := range mountsOf(before) {
		had[mountKey(m)] = true
	}
	// a volume Docker made for a declared folder carries its name: it is not new either
	for _, m := range before.Mounts {
		had[string(m.Type)+"|"+m.Name+"|"+m.Destination] = true
		had[string(m.Type)+"|"+m.Source+"|"+m.Destination] = true
	}
	if len(after.HostConfig.Mounts) > maxEditMounts {
		return fmt.Errorf("more than %d volumes and folders", maxEditMounts)
	}
	for _, m := range after.HostConfig.Mounts {
		if had[mountKey(m)] {
			continue
		}
		if cleanAbsolute(m.Target) == "" {
			return fmt.Errorf("the folder in the container %q must be an absolute path such as /config, without \"..\"", m.Target)
		}
		if err := checkVolumeOptions(m); err != nil {
			return err
		}
		switch m.Type {
		case mount.TypeBind:
			if cleanAbsolute(m.Source) == "" {
				return fmt.Errorf("the server folder %q must be an absolute path such as /mnt/data/app, without \"..\"", m.Source)
			}
		case mount.TypeVolume:
			if m.Source != "" && (len(m.Source) > 255 || !volumeNamePattern.MatchString(m.Source)) {
				return fmt.Errorf("the volume name %q may hold letters, digits and _ . - only, starting with a letter or a digit", m.Source)
			}
		case mount.TypeTmpfs:
		default:
			return fmt.Errorf("the type of mount %q: use a folder of the server (bind), a volume or tmpfs", m.Type)
		}
	}
	return nil
}

func deviceKey(host, inContainer string) string { return host + "|" + inContainer }

func checkDevices(before, after types.ContainerJSON) error {
	had := map[string]bool{}
	if before.HostConfig != nil {
		for _, d := range before.HostConfig.Devices {
			had[deviceKey(d.PathOnHost, d.PathInContainer)] = true
		}
	}
	if len(after.HostConfig.Devices) > maxEditDevices {
		return fmt.Errorf("more than %d devices", maxEditDevices)
	}
	for _, d := range after.HostConfig.Devices {
		if had[deviceKey(d.PathOnHost, d.PathInContainer)] {
			continue
		}
		host := cleanAbsolute(d.PathOnHost)
		if host == "" || !strings.HasPrefix(host, "/dev/") {
			return fmt.Errorf("the device %q: give a device of the server under /dev, for example /dev/dri/renderD128", d.PathOnHost)
		}
		if !deviceExists(host) {
			return fmt.Errorf("the device %s does not exist on this server", host)
		}
		if cleanAbsolute(d.PathInContainer) == "" {
			return fmt.Errorf("the device %s: its path in the container %q must be absolute, for example %s", host, d.PathInContainer, host)
		}
	}
	return nil
}

func checkDNS(before, after types.ContainerJSON) error {
	had := map[string]bool{}
	if before.HostConfig != nil {
		for _, s := range before.HostConfig.DNS {
			had[s] = true
		}
	}
	if len(after.HostConfig.DNS) > maxEditDNS {
		return fmt.Errorf("more than %d DNS servers", maxEditDNS)
	}
	for _, s := range after.HostConfig.DNS {
		if !had[s] && net.ParseIP(strings.TrimSpace(s)) == nil {
			return fmt.Errorf("the DNS server %q is not an IP address", s)
		}
	}
	return nil
}

func checkFields(before, after types.ContainerJSON) error {
	changed := func(a, b string) bool { return strings.TrimSpace(a) != strings.TrimSpace(b) }
	if after.Config != nil && before.Config != nil && changed(after.Config.User, before.Config.User) {
		if user := strings.TrimSpace(after.Config.User); user != "" && !userPattern.MatchString(user) {
			return fmt.Errorf("user %q: enter a user name or number, or user:group, for example 1000:1000", user)
		}
	}
	mode, oldMode := string(after.HostConfig.NetworkMode), ""
	if before.HostConfig != nil {
		oldMode = string(before.HostConfig.NetworkMode)
	}
	if changed(mode, oldMode) && mode != "" && !networkModePattern.MatchString(mode) {
		return fmt.Errorf("network mode %q: use bridge, host, none, the name of a network, or container:<name> to use another app's network", mode)
	}
	policy, oldPolicy := string(after.HostConfig.RestartPolicy.Name), ""
	if before.HostConfig != nil {
		oldPolicy = string(before.HostConfig.RestartPolicy.Name)
	}
	if changed(policy, oldPolicy) {
		switch policy {
		case "", "no", "always", "unless-stopped", "on-failure":
		default:
			return fmt.Errorf("restart %q: use no, always, unless-stopped or on-failure", policy)
		}
	}
	oldCPUs := int64(0)
	if before.HostConfig != nil {
		oldCPUs = before.HostConfig.NanoCPUs
	}
	if cpus := after.HostConfig.NanoCPUs; cpus != oldCPUs && cpus > 0 {
		if n := serverCPUs(); n > 0 && cpus > int64(n)*1e9 {
			return fmt.Errorf("CPUs %s: this server has %d", strconv.FormatFloat(float64(cpus)/1e9, 'f', -1, 64), n)
		}
	}
	return nil
}

// checkEdit says, in words, what of the edit the server refuses.
func checkEdit(before, after types.ContainerJSON) error {
	if after.HostConfig == nil {
		return errors.New("the container has no settings")
	}
	for _, check := range []func(types.ContainerJSON, types.ContainerJSON) error{checkPorts, checkMounts, checkDevices, checkDNS, checkFields} {
		if err := check(before, after); err != nil {
			return err
		}
	}
	return nil
}

// forkRefuseBadEdit answers 400 (DS025) with the reason, and true, when the
// edit holds a value the server refuses.
func forkRefuseBadEdit(w http.ResponseWriter, before types.ContainerJSON, after *types.ContainerJSON) bool {
	err := resolveNewBinds(before, after)
	if err == nil {
		err = checkEdit(before, *after)
	}
	if err == nil {
		return false
	}
	utils.HTTPError(w, "This change cannot be saved: "+err.Error()+". Nothing was changed.", http.StatusBadRequest, "DS025")
	return true
}

// bindRoot is where the server's files are seen from Cosmos.
func bindRoot() string {
	if utils.IsInsideContainer {
		return "/mnt/host"
	}
	return ""
}

// resolveSource follows a host path's links; replaced by the tests.
var resolveSource = func(p string) (string, bool, error) { return edithints.ResolvePath(bindRoot(), p) }

func inDataPlace(p string, places []string) bool {
	for _, place := range places {
		place = cleanAbsolute(place)
		if place != "" && place != "/" && within(p, place) {
			return true
		}
	}
	return false
}

// resolveNewBinds follows the links of each new or changed bind's source the
// way Docker will. A source without a link is left as it is. One that passes
// a link (an app can plant one in a folder it has) is handed to Docker as the
// path it leads to when that is on a data place, or when it is a sensitive
// folder (the tick then names the real folder); anything else is refused in
// words. From then on the checks, the tick, the folder's creation and Docker
// see the same path; what remains is the time between this look and Docker's
// mount at the container's start (and each later start): a folder inside a
// folder the app can write can be swapped for a link then, which is why the
// panel never proposes one.
func resolveNewBinds(before types.ContainerJSON, after *types.ContainerJSON) error {
	had := map[string]bool{}
	for _, m := range mountsOf(before) {
		had[mountKey(m)] = true
	}
	places := EditDataPlaces()
	for i, m := range after.HostConfig.Mounts {
		if m.Type != mount.TypeBind || had[mountKey(m)] {
			continue
		}
		source := cleanAbsolute(m.Source)
		if source == "" {
			continue // refused by checkMounts in words
		}
		resolved, linked, err := resolveSource(source)
		if err != nil {
			return fmt.Errorf("the server folder %s: its links cannot be followed (%s)", source, err.Error())
		}
		if !linked {
			continue
		}
		if inDataPlace(resolved, places) || SensitiveBind(resolved, extraSensitiveDirs()) != "" {
			after.HostConfig.Mounts[i].Source = resolved
			continue
		}
		return fmt.Errorf("the server folder %s is a link to %s, which is not on a data disk: give %s itself if that is the folder you mean", source, resolved, resolved)
	}
	return nil
}

// keepRestartRetries keeps the number of tries of "on-failure:N" when the
// tab sends the same policy back (it only knows the name; upstream reset
// the number to 0 on every save of the Docker tab).
func keepRestartRetries(before, after *types.ContainerJSON) {
	if before.HostConfig == nil || after.HostConfig == nil {
		return
	}
	if after.HostConfig.RestartPolicy.Name == before.HostConfig.RestartPolicy.Name && after.HostConfig.RestartPolicy.MaximumRetryCount == 0 {
		after.HostConfig.RestartPolicy.MaximumRetryCount = before.HostConfig.RestartPolicy.MaximumRetryCount
	}
}

// ---- the folder of a new bind ----

// EditDataPlaces are the places a new bind folder may be created in with
// the app's owner: the data path and the data disks. Set by package main
// (fork_edit_hints.go), which knows them.
var EditDataPlaces = func() []string { return nil }

// chownHandle gives a folder makeOwnedFolders has just created to the app's
// user, through the folder's own descriptor (fchown): no path is resolved
// again between the creation and the chown. Replaced by the tests, which do
// not run as root.
var chownHandle = func(fd int, uid, gid int) error { return unix.Fchown(fd, uid, gid) }

const folderOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// makeOwnedFolders creates the missing folders of source below place, one
// level at a time through directory descriptors: each level is made with
// mkdirat in its parent's descriptor and opened with openat(O_NOFOLLOW |
// O_DIRECTORY), and the next level is made in that descriptor. A link (one an
// app could have planted in its own folder, or swapped in meanwhile) is never
// followed: the opening fails and nothing more is made. Only a folder this
// call has just made is given to uid:gid (fchown on its descriptor; nothing
// when uid is 0); a folder that existed is left exactly as it is.
func makeOwnedFolders(root, place, source string, uid, gid int) error {
	if source == place || !strings.HasPrefix(source, place+"/") {
		return errors.New("not below the data place")
	}
	dir, err := unix.Open(root+place, folderOpenFlags, 0)
	if err != nil {
		return fmt.Errorf("the data place %s: %w", place, err)
	}
	defer func() { unix.Close(dir) }()
	for _, name := range strings.Split(strings.TrimPrefix(source, place+"/"), "/") {
		if name == "" || name == "." || name == ".." {
			return errors.New("not a clean path")
		}
		made := false
		switch err := unix.Mkdirat(dir, name, 0o755); {
		case err == nil:
			made = true
		case errors.Is(err, unix.EEXIST):
		default:
			return fmt.Errorf("making %s: %w", name, err)
		}
		next, err := unix.Openat(dir, name, folderOpenFlags, 0)
		if err != nil {
			return fmt.Errorf("%s is not a folder of its own (a link?): %w", name, err)
		}
		unix.Close(dir)
		dir = next
		if made && uid != 0 {
			if err := chownHandle(dir, uid, gid); err != nil {
				return fmt.Errorf("giving %s to the app's user: %w", name, err)
			}
		}
	}
	return nil
}

// RecipeEnvFor names the variables Cosmos' recipe of an image sets (a
// recipe that names PUID makes an image PUID-style); set by package main
// from src/anyimage, which this package does not import.
var RecipeEnvFor = func(image string) []string { return nil }

// ContainerRunsAs is the user a container writes its files as: the one rule
// for the update route's new folders and for the panel (the edit hints send
// its result). imageUser only says where the user came from.
func ContainerRunsAs(c types.ContainerJSON, imageUser string) edithints.RunAs {
	if c.Config == nil {
		return edithints.RunAs{}
	}
	return edithints.EffectiveUser(c.Config.User, imageUser, c.Config.Env, edithints.LinuxserverStyle(c.Config.Image, RecipeEnvFor(c.Config.Image)))
}

func containerRunsAs(c types.ContainerJSON) edithints.RunAs { return ContainerRunsAs(c, "") }

// forkPrepareBindFolders creates the source folder of each new bind that
// does not exist and lies under a data place, owned by the container's user
// when that is a number other than root. Everything else is left to
// EditContainer (which creates a missing folder owned by root).
func forkPrepareBindFolders(before, after types.ContainerJSON, root string) {
	had := map[string]bool{}
	for _, m := range mountsOf(before) {
		had[m.Source] = true
	}
	user := containerRunsAs(after)
	uid, gid := 0, 0 // a user given by name, or root: the folder stays root's, as EditContainer would make it
	if user.Known && user.UID != 0 {
		uid, gid = user.UID, user.GID
		if gid < 0 {
			gid = user.UID
		}
	}
	places := EditDataPlaces()
	for _, m := range after.HostConfig.Mounts {
		if m.Type != mount.TypeBind || had[m.Source] {
			continue
		}
		source := cleanAbsolute(m.Source)
		if source == "" {
			continue
		}
		folder := edithints.InspectFolder(root, source, places, user)
		if folder.State != "missing" {
			continue
		}
		// a data path that does not exist yet (before the first installation) is made as
		// EditContainer would make it: the administrator's (root), 0755
		if _, err := os.Lstat(root + folder.Place); os.IsNotExist(err) {
			if err := os.MkdirAll(root+folder.Place, 0o755); err != nil {
				utils.Error("Edit: making the data path", err)
				continue
			}
		}
		if err := makeOwnedFolders(root, folder.Place, source, uid, gid); err != nil {
			utils.Error("Edit: making the folder of a new bind for the app's user", err)
		}
	}
}
