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
	"sort"
	"strconv"
	"strings"
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
