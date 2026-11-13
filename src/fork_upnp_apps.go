package main

// Fork addition: where the server meets the router port forwarding for the
// ports of applications (package upnp): its routes, and the list of
// containers with what they publish (upnp does not import the Docker client).

import (
	"os"
	"strings"

	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

// registerUPnPRoutes adds the routes to the admin router: behind the token
// check, the SmartShield and the admin address restriction. Each checks its
// permission itself: reading the status takes PERM_CONFIGURATION_READ,
// everything that changes the router PERM_ADMIN (the confirmed password).
func registerUPnPRoutes(admin *mux.Router) {
	admin.HandleFunc("/api/upnp", upnp.StatusRoute)
	admin.HandleFunc("/api/upnp/apps", upnp.AppPortRoute)
	admin.HandleFunc("/api/upnp/close", upnp.CloseRoute)
}

func init() {
	upnp.ListContainers = func() ([]upnp.Container, error) {
		containers, err := docker.ListContainers()
		if err != nil {
			return nil, err
		}
		self := ""
		if utils.IsInsideContainer {
			self = os.Getenv("HOSTNAME")
		}
		list := make([]upnp.Container, 0, len(containers))
		for _, container := range containers {
			if len(container.Names) == 0 {
				continue
			}
			item := upnp.Container{
				Name:    strings.TrimPrefix(container.Names[0], "/"),
				Running: container.State == "running",
			}
			item.IsCosmos = self != "" && (strings.HasPrefix(container.ID, self) || item.Name == self)
			for _, port := range container.Ports {
				if port.PublicPort == 0 {
					continue
				}
				item.Ports = append(item.Ports, upnp.Published{
					ContainerPort: int(port.PrivatePort),
					Protocol:      strings.ToLower(port.Type),
					HostPort:      int(port.PublicPort),
					HostIP:        port.IP,
				})
			}
			list = append(list, item)
		}
		return list, nil
	}
}
