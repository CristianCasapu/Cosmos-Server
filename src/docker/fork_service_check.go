package docker

// Fork addition: what a request to create services is checked for before
// anything is pulled, created or set aside (CreateServiceRoute).
//
//   - Ports and devices the create loop would crash on: a port without a host
//     part ("8080"), a reversed or open range, text that is not a number, a
//     device without ":". The loop indexed and allocated from them unchecked,
//     panicking half-way: the rollback never ran, so earlier services of the
//     same request were left stopped and renamed.
//   - A container that already exists under one of the names: the create
//     replaced it silently. Replacing is what an edit does, so it is allowed
//     only when the request says so (?replace=1, sent by the edit form).

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/go-units"
)

// portRange reads "8080" or "8000-8010": first and last port.
func portRange(text string) (int, int, error) {
	parts := strings.Split(text, "-")
	if len(parts) > 2 {
		return 0, 0, errors.New("not a port or a range of ports")
	}
	numbers := []int{}
	for _, part := range parts {
		number, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || number < 1 || number > 65535 {
			return 0, 0, errors.New("not a port between 1 and 65535")
		}
		numbers = append(numbers, number)
	}
	if len(numbers) == 1 {
		return numbers[0], numbers[0], nil
	}
	if numbers[1] < numbers[0] {
		return 0, 0, errors.New("a range must go from the lower port to the higher")
	}
	return numbers[0], numbers[1], nil
}

// checkPortSpec checks one entry of a service's ports the way the create
// loop reads it: "[ip:]host:container[/tcp|udp]".
func checkPortSpec(spec string) error {
	main, protocol, hasProtocol := strings.Cut(spec, "/")
	if hasProtocol && protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
		return errors.New("the protocol must be tcp or udp")
	}
	parts := strings.Split(main, ":")
	if len(parts) < 2 {
		return errors.New("give the port on the server as well, as \"8080:80\" (a port without one would be published on a random port)")
	}
	if len(parts) > 2 {
		ip := strings.Trim(strings.Join(parts[:len(parts)-2], ":"), "[]")
		if net.ParseIP(ip) == nil {
			return errors.New("what comes before the two ports must be an IP address")
		}
	}
	hostFirst, hostLast, err := portRange(parts[len(parts)-2])
	if err != nil {
		return errors.New("port on the server: " + err.Error())
	}
	contFirst, contLast, err := portRange(parts[len(parts)-1])
	if err != nil {
		return errors.New("port of the container: " + err.Error())
	}
	if hostLast-hostFirst > 1000 || contLast-contFirst > 1000 {
		return errors.New("a range of more than 1000 ports")
	}
	return nil
}

// CheckServiceRequest refuses what the create loop cannot handle, naming
// the service and the value.
func CheckServiceRequest(request DockerServiceCreateRequest) error {
	keys := make([]string, 0, len(request.Services))
	for key := range request.Services {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		service := request.Services[key]
		for _, port := range service.Ports {
			if err := checkPortSpec(strings.TrimSpace(port)); err != nil {
				return fmt.Errorf("service %s, port %q: %s", key, port, err.Error())
			}
		}
		for _, device := range service.Devices {
			parts := strings.Split(device, ":")
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("service %s, device %q: write it as /dev/on-the-server:/dev/in-the-container", key, device)
			}
		}
		if err := checkServiceFields(service); err != nil {
			return fmt.Errorf("service %s, %s", key, err.Error())
		}
	}
	return nil
}

// userPattern: a name, a number, or user:group with either part a name or a number.
var userPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,31})?$`)

var networkModePattern = regexp.MustCompile(`^(bridge|host|none|default|(container|service):[A-Za-z0-9][A-Za-z0-9_.-]{0,127}|[A-Za-z0-9][A-Za-z0-9_.-]{0,127})$`)

// serverCPUs is how many CPUs the daemon has, 0 when not known. Replaced by the tests.
var serverCPUs = func() int {
	if DockerClient == nil {
		return 0
	}
	info, err := DockerClient.Info(DockerContext)
	if err != nil {
		return 0
	}
	return info.NCPU
}

// checkServiceFields checks the fields of the new-container form (and the
// same keys of a compose file) the way Docker will read them, so that a bad
// value is said before anything is created instead of half-way.
func checkServiceFields(service ContainerCreateRequestContainer) error {
	if service.User != "" && !userPattern.MatchString(service.User) {
		return fmt.Errorf("user %q: enter a user name or number, or user:group, for example 1000:1000", service.User)
	}
	for name, value := range map[string]string{"memory limit": service.MemLimit, "memory reservation": service.MemReservation} {
		if value == "" {
			continue
		}
		size, err := units.RAMInBytes(value)
		if err != nil || size < 0 {
			return fmt.Errorf("%s %q: enter a size such as 512m or 2g", name, value)
		}
		if name == "memory limit" && size > 0 && size < 6*1024*1024 {
			return fmt.Errorf("memory limit %q: Docker needs at least 6m", value)
		}
	}
	if service.CPUs < 0 {
		return fmt.Errorf("CPUs %v: enter 0 for no limit, or a number such as 0.5 or 2", service.CPUs)
	}
	if service.CPUs > 0 {
		if cpus := serverCPUs(); cpus > 0 && service.CPUs > float64(cpus) {
			return fmt.Errorf("CPUs %v: this server has %d", service.CPUs, cpus)
		}
	}
	mode := effectiveNetworkMode(service)
	if mode != "" && !networkModePattern.MatchString(mode) {
		return fmt.Errorf("network mode %q: use bridge, host, none, the name of a network, or container:<name> to use another app's network", mode)
	}
	if strings.HasPrefix(mode, "container:") || strings.HasPrefix(mode, "service:") {
		if len(service.Ports) > 0 {
			return fmt.Errorf("uses the network of %s, so it cannot publish ports of its own: publish them on %s", strings.SplitN(mode, ":", 2)[1], strings.SplitN(mode, ":", 2)[1])
		}
	}
	if policy := strings.TrimSpace(service.RestartPolicy); policy != "" {
		name, retries, found := strings.Cut(policy, ":")
		switch name {
		case "no", "always", "unless-stopped", "on-failure":
		default:
			return fmt.Errorf("restart %q: use no, always, unless-stopped or on-failure", policy)
		}
		if found {
			if count, err := strconv.Atoi(retries); name != "on-failure" || err != nil || count < 0 {
				return fmt.Errorf("restart %q: only on-failure takes a number of tries, as on-failure:5", policy)
			}
		}
	}
	if _, err := service.ShmSize.bytes(); err != nil {
		return errors.New("shm_size: " + err.Error())
	}
	for _, entry := range service.Tmpfs.list {
		if !strings.HasPrefix(entry, "/") {
			return fmt.Errorf("tmpfs %q: give the folder in the container, starting with /", entry)
		}
	}
	return nil
}

// containerExists is replaced by the tests.
var containerExists = func(name string) bool {
	_, err := DockerClient.ContainerInspect(DockerContext, name)
	return err == nil
}

// ExistingServiceNames lists the containers of the request that exist already.
func ExistingServiceNames(request DockerServiceCreateRequest) []string {
	found := []string{}
	for key, service := range request.Services {
		name := service.Name
		if name == "" {
			name = key
		}
		if containerExists(name) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}

// forkRefuseService checks a create request before it starts. It answers in
// the stream's own words ("[OPERATION FAILED]") and returns false when the
// request is refused.
func forkRefuseService(w http.ResponseWriter, req *http.Request, request DockerServiceCreateRequest, flush func()) bool {
	if err := CheckServiceRequest(request); err != nil {
		fmt.Fprintf(w, "%s", "[OPERATION FAILED] "+err.Error()+"\n")
		flush()
		return false
	}
	// access to the server that needs the admin's yes, item by item (fork_sensitive_access.go)
	if missing := unconfirmedSensitive(request, req.URL.Query()); len(missing) > 0 {
		fmt.Fprintf(w, "%s", "[OPERATION FAILED] This definition asks for access to the server that needs your explicit confirmation on the review: "+
			sensitiveSentence(missing)+". Nothing was created.\n")
		flush()
		return false
	}
	// an image made for another processor is said before the pull (fork_image_arch.go)
	if req.URL.Query().Get("anyArch") != "1" {
		if refusal := platformRefusal(request); refusal != "" {
			fmt.Fprintf(w, "%s", "[OPERATION FAILED] "+refusal+"\n")
			flush()
			return false
		}
	}
	if req.URL.Query().Get("replace") != "1" {
		if existing := ExistingServiceNames(request); len(existing) > 0 {
			fmt.Fprintf(w, "%s", "[OPERATION FAILED] A container named "+strings.Join(existing, ", ")+
				" exists already: creating it again would replace it. Pick another name, or change that app from its own page.\n")
			flush()
			return false
		}
	}
	return true
}
