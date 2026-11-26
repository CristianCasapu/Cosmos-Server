package appsignin

// Fork addition (plan 47, I2e): what the monitoring database form
// (Configuration > External, client/src/pages/config/configExternal.jsx)
// proposes: the PostgreSQL containers that run on this server, with the
// address Cosmos reaches them at, their database and their user.
//
//	GET /cosmos/api/config/postgres-proposals    (credentials-read, like the sign-in card)
//
// The database and the user come from the sign-in details of each container
// (resolve, then clean): never a password. A password is only named by its
// secret id ("password.0"); the form fetches it through the recorded reveal
// route, on a click of the administrator.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	conttype "github.com/docker/docker/api/types/container"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	maxPostgresProposals = 20
	postgresPort         = "5432/tcp"
)

// pgCandidate is a running container of a PostgreSQL image, as read from Docker.
type pgCandidate struct {
	Info containerInfo
	// Published: the host addresses and ports 5432/tcp is published on
	Published []pgBinding
	// Addresses: the container's own addresses on its networks
	Addresses []string
	// Networks: the names of its networks
	Networks []string
}

type pgBinding struct {
	HostIP   string
	HostPort string
}

// PostgresProposal is one proposal of the form; it holds no secret.
type PostgresProposal struct {
	Container string `json:"container"`
	Image     string `json:"image"`
	Host      string `json:"host"`
	// HostSource: published (a port of the server), container-network (Cosmos
	// runs in a container and reaches it by name), address (the container's
	// own address, which changes when the container is made again)
	HostSource string `json:"hostSource"`
	HostNote   string `json:"hostNote,omitempty"`
	// Addresses: the container's own addresses on its Docker networks, so
	// that the form can tell a saved server that is one of them (plan 51)
	Addresses []string `json:"addresses,omitempty"`
	// Reach: answers / silent, a TCP connect from Cosmos to Host (plan 51)
	Reach          string `json:"reach,omitempty"`
	Database       string `json:"database,omitempty"`
	User           string `json:"user,omitempty"`
	PasswordSecret string `json:"passwordSecret,omitempty"`
	// Source: where the details came from (recipe, label, guess)
	Source string `json:"source"`
}

// isPostgresImage: the image's recipe provides PostgreSQL.
func isPostgresImage(image string) bool {
	recipe := anyimage.RecipeForImage(image)
	return recipe != nil && recipe.Provides == "postgres"
}

// postgresCandidates lists the running PostgreSQL containers; replaced by the tests.
var postgresCandidates = func(ctx context.Context) ([]pgCandidate, error) {
	if err := docker.Connect(); err != nil {
		return nil, err
	}
	list, err := docker.DockerClient.ContainerList(ctx, conttype.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := []pgCandidate{}
	for _, item := range list {
		if len(out) >= maxPostgresProposals {
			break
		}
		if item.State != "running" || !isPostgresImage(item.Image) {
			continue
		}
		full, err := docker.DockerClient.ContainerInspect(ctx, item.ID)
		if err != nil || full.Config == nil || full.State == nil || !full.State.Running {
			continue
		}
		c := pgCandidate{Info: containerInfo{Name: full.Name, Image: full.Config.Image, Env: full.Config.Env, Labels: map[string]string{}}}
		for key, value := range full.Config.Labels {
			c.Info.Labels[key] = value
		}
		for _, key := range []string{LabelKey, ManagerLabel} {
			if value := docker.ForkAdminLabel(full, key); value != "" {
				c.Info.Labels[key] = value
			} else {
				delete(c.Info.Labels, key)
			}
		}
		if full.NetworkSettings != nil {
			for port, bindings := range full.NetworkSettings.Ports {
				if string(port) != postgresPort {
					continue
				}
				for _, b := range bindings {
					c.Published = append(c.Published, pgBinding{HostIP: b.HostIP, HostPort: b.HostPort})
				}
			}
			names := make([]string, 0, len(full.NetworkSettings.Networks))
			for name := range full.NetworkSettings.Networks {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				c.Networks = append(c.Networks, name)
				if n := full.NetworkSettings.Networks[name]; n != nil && n.IPAddress != "" {
					c.Addresses = append(c.Addresses, n.IPAddress)
				}
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// insideContainer tells whether Cosmos runs in a container; replaced by the tests.
var insideContainer = func() bool { return utils.IsInsideContainer }

// cosmosNetworks are the Docker networks of Cosmos' own container (when it
// runs in one); replaced by the tests.
var cosmosNetworks = func(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	self := os.Getenv("HOSTNAME")
	if self == "" || docker.Connect() != nil {
		return out
	}
	full, err := docker.DockerClient.ContainerInspect(ctx, self)
	if err != nil || full.NetworkSettings == nil {
		return out
	}
	for name := range full.NetworkSettings.Networks {
		out[name] = true
	}
	return out
}

// The notes of the host choices (the panel has the same sentences under
// mgmt.config.general.monitoringDb.fork.note.*).
const (
	noteAddress  = "The container's own address on Docker's network: it works now, but it changes when the container is made again (an update, an edit) or the containers start in another order. Better: publish its port 5432 on 127.0.0.1 (in the container's Network tab), then use 127.0.0.1:5432."
	noteLAN      = "Its port is published on this address of the server only (not on 127.0.0.1)."
	noteNetwork  = "Its name on a Docker network Cosmos shares with it."
	noteNoShared = "Cosmos runs in a container and shares no Docker network with it: open the container's page and use \"Let these apps reach it\" to put Cosmos on its network, or publish its port 5432."
)

// bindingHost is the address of the server a published port is reached at
// from the server itself, "" when the binding is not usable.
func bindingHost(b pgBinding) (string, bool) {
	port, err := strconv.Atoi(b.HostPort)
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	switch b.HostIP {
	case "", "0.0.0.0", "::":
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), true
	}
	ip := net.ParseIP(b.HostIP)
	if ip == nil {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), ip.IsLoopback()
}

// hostFor picks the address Cosmos reaches the database at, for where
// Cosmos runs: (host, source, note). source: published (a port of the
// server), container-network (by name, Cosmos in a container on a shared
// network), address (the container's own address), none (no way found).
func hostFor(c pgCandidate, name string, inside bool, shared map[string]bool) (string, string, string) {
	if inside {
		for _, network := range c.Networks {
			if shared[network] {
				return name + ":5432", "container-network", noteNetwork
			}
		}
		// a port published on an address of the server that is not the
		// loopback is reached from a container too; 127.0.0.1 is not
		for _, b := range c.Published {
			if host, _ := bindingHost(b); host != "" && b.HostIP != "" && b.HostIP != "0.0.0.0" && b.HostIP != "::" {
				if ip := net.ParseIP(b.HostIP); ip != nil && !ip.IsLoopback() {
					return host, "published", noteLAN
				}
			}
		}
		return "", "none", noteNoShared
	}
	// bare-metal: every address of the server or its loopback first, then
	// the one address it is bound to
	other := ""
	for _, b := range c.Published {
		host, loopback := bindingHost(b)
		if host == "" {
			continue
		}
		if loopback {
			return host, "published", ""
		}
		if other == "" {
			other = host
		}
	}
	if other != "" {
		return other, "published", noteLAN
	}
	for _, address := range c.Addresses {
		if ip := net.ParseIP(address); ip != nil {
			return net.JoinHostPort(ip.String(), "5432"), "address", noteAddress
		}
	}
	return "", "", ""
}

// postgresProposals builds the proposals from what Docker answered.
func postgresProposals(candidates []pgCandidate, inside bool, shared map[string]bool) []PostgresProposal {
	out := []PostgresProposal{}
	for _, c := range candidates {
		if len(out) >= maxPostgresProposals {
			break
		}
		name := strings.TrimPrefix(c.Info.Name, "/")
		if !validContainerName(name) || !isPostgresImage(c.Info.Image) {
			continue
		}
		details := resolve(c.Info, nil, func(string) string { return "" })
		clean(&details, details.secretValues)
		p := PostgresProposal{Container: name, Image: shown(c.Info.Image), Source: details.Source, Addresses: append([]string{}, c.Addresses...)}
		p.Host, p.HostSource, p.HostNote = hostFor(c, name, inside, shared)
		if p.Host == "" && p.HostSource != "none" {
			continue
		}
		if len(details.Accounts) > 0 {
			a := details.Accounts[0]
			p.Database, p.User, p.PasswordSecret = a.Database, a.User, a.Password
			// the recipe's default database is the user's name when none is set
			if p.Database == "" && p.User != "" {
				p.Database = p.User
			}
			// the last check of this exit, as the card's: nothing that holds a secret value
			for _, s := range []*string{&p.Database, &p.User} {
				if holdsSecret(*s, details.secretValues) {
					*s = ""
				}
			}
		}
		out = append(out, p)
	}
	return out
}

// PostgresProposalsRoute godoc
// @Summary Monitoring database: the PostgreSQL containers of this server, proposed
// @Description Lists the running containers whose image is a PostgreSQL, each with the server address Cosmos would use, its database and user from the container's settings, and the id of its password for the sign-in reveal. Never a password. Needs the permission to read credentials.
// @Tags config
// @Produce json
// @Security BearerAuth
// @Success 200 {object} utils.APIResponse
// @Failure 401 {object} utils.HTTPErrorResult
// @Failure 403 {object} utils.HTTPErrorResult
// @Failure 405 {object} utils.HTTPErrorResult
// @Failure 503 {object} utils.HTTPErrorResult
// @Router /api/config/postgres-proposals [get]
func PostgresProposalsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_CREDENTIALS_READ) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	candidates, err := postgresCandidates(ctx)
	if err != nil {
		utils.HTTPError(w, "Docker does not answer: the PostgreSQL containers cannot be listed", http.StatusServiceUnavailable, "SI010")
		return
	}
	proposals := checkedProposals(ctx, candidates)
	noStore(w)
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": map[string]interface{}{
		"insideContainer": insideContainer(),
		"proposals":       proposals,
	}})
}
