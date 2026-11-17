package cron

// Fork addition: common jobs the scheduled-job form proposes for a container.
//
//	GET /cosmos/api/cron-proposals/{containerId}
//
// For a database whose image has a recipe with a backup line
// (src/anyimage/recipes.json: the PostgreSQL family's pg_dumpall, the MySQL
// and MariaDB family's mysqldump / mariadb-dump), a nightly dump into the
// container's data folder, kept two weeks. The command is made from the
// recipe's variable names and the mount's place in the container only: the
// shell reads the user and the password from the container's own variables
// when the job runs, so the command holds no value of the container, and
// nothing of the request goes into it. PERM_RESOURCES, like saving a job.

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/gorilla/mux"

	"github.com/azukaar/cosmos-server/src/anyimage"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	dumpFolderName = "cosmos-dumps"
	dumpKeepDays   = 14
	dumpSchedule   = "0 0 3 * * *"
)

var (
	envVarName   = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	plainUser    = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	plainPathRe  = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)
	dumpBinaries = map[string]string{"pg_dumpall": "postgres", "mariadb-dump": "mysql", "mysqldump": "mysql"}
)

// proposalMount is a mount of the container: where it is in the container
// and where its files are on the server.
type proposalMount struct {
	Source      string
	Destination string
}

// proposalContainer is what the proposals need of a container: the names of
// its variables, never their values.
type proposalContainer struct {
	Name    string
	Image   string
	Running bool
	EnvSet  map[string]bool
	Mounts  []proposalMount
}

// inspectForProposals reads a container; replaced by the tests.
var inspectForProposals = func(ctx context.Context, name string) (proposalContainer, error) {
	if err := docker.Connect(); err != nil {
		return proposalContainer{}, err
	}
	c, err := docker.DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		return proposalContainer{}, err
	}
	return proposalContainerOf(c), nil
}

// proposalContainerOf keeps of an inspected container what the proposals
// need; the values of its variables are dropped here.
func proposalContainerOf(c types.ContainerJSON) proposalContainer {
	info := proposalContainer{Name: strings.TrimPrefix(c.Name, "/"), EnvSet: map[string]bool{}}
	if c.ContainerJSONBase != nil && c.State != nil {
		info.Running = c.State.Running
	}
	if c.Config != nil {
		info.Image = c.Config.Image
		for _, entry := range c.Config.Env {
			key, value, _ := strings.Cut(entry, "=")
			info.EnvSet[key] = value != ""
		}
	}
	for _, m := range c.Mounts {
		info.Mounts = append(info.Mounts, proposalMount{Source: m.Source, Destination: m.Destination})
	}
	return info
}

// JobProposal is one job the form offers.
type JobProposal struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // postgres, mysql
	Name     string `json:"name"`
	Command  string `json:"command"`
	Schedule string `json:"schedule"`
	// where the dumps go, inside the container and on the server
	FolderInContainer string `json:"folderInContainer"`
	FolderOnServer    string `json:"folderOnServer,omitempty"`
	KeepDays          int    `json:"keepDays"`
	// User: the database account the dump signs in with, as a variable ("$POSTGRES_USER") or a fixed name
	User string `json:"user"`
}

// JobProposals is the route's answer.
type JobProposals struct {
	Container string        `json:"container"`
	Image     string        `json:"image"`
	Running   bool          `json:"running"`
	Recipe    string        `json:"recipe,omitempty"`
	Proposals []JobProposal `json:"proposals"`
	// Note: why there is no proposal: "noRecipe" (no known backup for this image), "noDataFolder" (the data
	// folder of the recipe is not a mount: a dump would be lost with the container)
	Note string `json:"note,omitempty"`
}

// dumpAccount is the account a dump signs in with: the first of root, then
// the others, whose password variable is set (or that needs none).
func dumpAccount(recipe *anyimage.Recipe, c proposalContainer) (anyimage.RecipeLoginAccount, bool) {
	if recipe.Login == nil {
		return anyimage.RecipeLoginAccount{}, false
	}
	ordered := []anyimage.RecipeLoginAccount{}
	for _, a := range recipe.Login.Accounts {
		if a.Role == "root" {
			ordered = append(ordered, a)
		}
	}
	for _, a := range recipe.Login.Accounts {
		if a.Role != "root" {
			ordered = append(ordered, a)
		}
	}
	for _, a := range ordered {
		if a.Password != "" && !c.EnvSet[a.Password] {
			continue
		}
		return a, true
	}
	return anyimage.RecipeLoginAccount{}, false
}

// userWord is how the command names the account: the variable with its
// default, or the fixed name; "" when neither is a plain name.
func userWord(a anyimage.RecipeLoginAccount) (string, string) {
	if a.User != "" && envVarName.MatchString(a.User) {
		fallback := a.UserUnset
		if fallback != "" && !plainUser.MatchString(fallback) {
			fallback = ""
		}
		if fallback != "" {
			return `"${` + a.User + `:-` + fallback + `}"`, "$" + a.User
		}
		return `"$` + a.User + `"`, "$" + a.User
	}
	name := a.UserFixed
	if name == "" {
		name = a.UserUnset
	}
	if plainUser.MatchString(name) {
		return name, name
	}
	return "", ""
}

// dataMount is the container's mount of a recipe volume marked for backup.
func dataMount(recipe *anyimage.Recipe, c proposalContainer) (proposalMount, bool) {
	for _, v := range recipe.Volumes {
		if !v.Backup {
			continue
		}
		for _, m := range c.Mounts {
			if path.Clean(m.Destination) == path.Clean(v.Target) && plainPathRe.MatchString(path.Clean(v.Target)) {
				return m, true
			}
		}
	}
	return proposalMount{}, false
}

// proposalsFor builds the answer for one container.
func proposalsFor(c proposalContainer) JobProposals {
	answer := JobProposals{Container: c.Name, Image: c.Image, Running: c.Running, Proposals: []JobProposal{}}
	recipe := anyimage.RecipeForImage(c.Image)
	if recipe == nil || recipe.Backup == "" {
		answer.Note = "noRecipe"
		return answer
	}
	answer.Recipe = recipe.Name
	binary := strings.Fields(recipe.Backup)[0]
	kind, known := dumpBinaries[binary]
	if !known {
		answer.Note = "noRecipe"
		return answer
	}
	account, ok := dumpAccount(recipe, c)
	if !ok {
		answer.Note = "noRecipe"
		return answer
	}
	user, userShown := userWord(account)
	if user == "" {
		answer.Note = "noRecipe"
		return answer
	}
	mount, ok := dataMount(recipe, c)
	if !ok {
		answer.Note = "noDataFolder"
		return answer
	}
	folder := path.Join(path.Clean(mount.Destination), dumpFolderName)
	file := folder + "/dump-$(date +%Y%m%d-%H%M%S).sql"

	password := ""
	if account.Password != "" && envVarName.MatchString(account.Password) {
		variable := "PGPASSWORD"
		if kind == "mysql" {
			variable = "MYSQL_PWD"
		}
		password = variable + `="$` + account.Password + `" `
	}

	var dump string
	if kind == "postgres" {
		dump = password + "pg_dumpall -U " + user + " -f " + file
	} else {
		databases := "--all-databases"
		if account.Role != "root" {
			if account.Database == "" || !envVarName.MatchString(account.Database) {
				answer.Note = "noRecipe"
				return answer
			}
			databases = `--databases "$` + account.Database + `"`
		}
		dump = password + binary + " " + databases + " -u " + user + " --result-file=" + file
	}
	command := "mkdir -p " + folder + " && " + dump + " && find " + folder + " -name 'dump-*.sql' -mtime +" + strconv.Itoa(dumpKeepDays-1) + " -delete"

	proposal := JobProposal{
		ID:                "dump",
		Kind:              kind,
		Name:              "Nightly dump of " + c.Name,
		Command:           command,
		Schedule:          dumpSchedule,
		FolderInContainer: folder,
		KeepDays:          dumpKeepDays,
		User:              userShown,
	}
	if strings.HasPrefix(mount.Source, "/") {
		proposal.FolderOnServer = path.Join(path.Clean(mount.Source), dumpFolderName)
	}
	if len(proposal.Name) > maxJobName {
		proposal.Name = proposal.Name[:maxJobName]
	}
	answer.Proposals = append(answer.Proposals, proposal)
	return answer
}

// ProposalsRoute answers GET /cosmos/api/cron-proposals/{containerId}.
func ProposalsRoute(w http.ResponseWriter, req *http.Request) {
	if utils.CheckPermissions(w, req, utils.PERM_RESOURCES) != nil {
		return
	}
	if req.Method != "GET" {
		utils.HTTPError(w, "Method not allowed", http.StatusMethodNotAllowed, "HTTP001")
		return
	}
	name := strings.TrimPrefix(mux.Vars(req)["containerId"], "/")
	if !jobContainerName.MatchString(name) {
		utils.HTTPError(w, "Invalid container name", http.StatusBadRequest, "CR040")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), containerCheck)
	defer cancel()
	c, err := inspectForProposals(ctx, name)
	if err != nil {
		utils.HTTPError(w, "No such container, or Docker does not answer", http.StatusNotFound, "CR041")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "OK", "data": proposalsFor(c)})
}
