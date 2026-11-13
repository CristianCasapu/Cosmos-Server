package docker

// Fork addition: an edit keeps the old container until its replacement runs.
//
// Upstream stopped and removed the container, then created the new one, and
// when that failed tried to create the old one again from what `inspect` had
// said about it. That second creation is another container (network aliases
// and fixed addresses are not carried over), and when it fails as well the
// app is gone. Here the old container is stopped and renamed instead. It is
// removed only once its replacement has been created and started; when that
// fails the replacement is removed and the old container gets its name back
// and is started again if it ran before.

import (
	"errors"
	"strings"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
	conttype "github.com/docker/docker/api/types/container"
)

// asideMarker sits between a container's name and the start of its ID while
// it is set aside: "nextcloud" becomes "nextcloud-cosmos-old-1a2b3c4d5e6f".
const asideMarker = "-cosmos-old-"

// containerSwapper is what setting a container aside needs from Docker. The
// tests replace it with a fake, Docker not being available to them.
type containerSwapper interface {
	Stop(id string) error
	Start(id string) error
	Rename(id string, name string) error
	Remove(id string, force bool) error
	// IDByName returns the ID of the container with that name, "" when there is none.
	IDByName(name string) string
}

type dockerSwapper struct{}

func (dockerSwapper) Stop(id string) error {
	return DockerClient.ContainerStop(DockerContext, id, conttype.StopOptions{})
}

func (dockerSwapper) Start(id string) error {
	return DockerClient.ContainerStart(DockerContext, id, conttype.StartOptions{})
}

func (dockerSwapper) Rename(id string, name string) error {
	return DockerClient.ContainerRename(DockerContext, id, name)
}

func (dockerSwapper) Remove(id string, force bool) error {
	return DockerClient.ContainerRemove(DockerContext, id, conttype.RemoveOptions{Force: force})
}

func (dockerSwapper) IDByName(name string) string {
	found, err := DockerClient.ContainerInspect(DockerContext, name)
	if err != nil || found.ContainerJSONBase == nil {
		return ""
	}
	return found.ID
}

// swapper is the Docker used to set containers aside.
var swapper containerSwapper = dockerSwapper{}

// renameRetryDelay is how long to wait before asking again for a name Docker
// has not released yet.
var renameRetryDelay = 500 * time.Millisecond

// asideContainer is a container that was stopped and renamed to make room for
// its replacement.
type asideContainer struct {
	id         string
	name       string // the name it had, without the leading slash
	asideName  string
	wasRunning bool
	settled    bool // put back or dropped: nothing more to do, whoever asks
}

func asideNameFor(name string, id string) string {
	short := id
	if len(short) > 12 {
		short = short[:12]
	}
	return name + asideMarker + short
}

// setContainerAside stops the container and renames it, so that a replacement
// can be created under its name. When that cannot be done the container is
// left as it was (started again if it ran) and the reason is returned.
func setContainerAside(old types.ContainerJSON) (*asideContainer, error) {
	if old.ContainerJSONBase == nil || old.ID == "" {
		return nil, errors.New("Cannot change a container that was not found")
	}

	aside := &asideContainer{
		id:         old.ID,
		name:       strings.TrimPrefix(old.Name, "/"),
		wasRunning: old.State != nil && old.State.Running,
	}
	if aside.name == "" {
		return nil, errors.New("Cannot change a container that has no name")
	}
	aside.asideName = asideNameFor(aside.name, aside.id)

	// Docker removes a container started with --rm as soon as it stops:
	// nothing would be left to go back to, and nothing to rename.
	if aside.wasRunning && old.HostConfig != nil && old.HostConfig.AutoRemove {
		return nil, errors.New("This container is removed by Docker as soon as it stops (it was started with --rm), so it cannot be changed while it runs")
	}

	if err := swapper.Stop(aside.id); err != nil {
		return nil, err
	}

	if err := swapper.Rename(aside.id, aside.asideName); err != nil {
		if aside.wasRunning && !containerGone(err) {
			if startErr := swapper.Start(aside.id); startErr != nil {
				utils.Error("EditContainer - the container was stopped for a change that could not be made, and could not be started again", startErr)
				return nil, errors.New(err.Error() + ". The container was stopped and could not be started again: " + startErr.Error())
			}
		}
		return nil, err
	}

	utils.Log("EditContainer - Container " + aside.name + " stopped and kept as " + aside.asideName + " until its replacement runs")
	return aside, nil
}

// putBack undoes setContainerAside: it removes the replacement (its ID, empty
// when none was created), gives the container its name back and starts it if
// it ran before. The error says what is left when that fails.
func (aside *asideContainer) putBack(replacement string) error {
	if aside.settled {
		return nil
	}

	// never the container that is being put back
	if replacement != "" && replacement != aside.id {
		if err := swapper.Remove(replacement, true); err != nil && !containerGone(err) {
			utils.Error("EditContainer - Failed to remove the replacement of "+aside.name, err)
			return errors.New("The replacement could not be removed (" + err.Error() + "). The previous container is kept, stopped, as " + aside.asideName)
		}
	}

	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = swapper.Rename(aside.id, aside.name)
		// the name of a container that was just removed can stay taken for a moment
		if err == nil || !strings.Contains(err.Error(), "already in use") {
			break
		}
		time.Sleep(renameRetryDelay)
	}
	if err != nil {
		utils.Error("EditContainer - Failed to give "+aside.name+" its name back", err)
		return errors.New("The previous container could not get its name back (" + err.Error() + "). It is kept, stopped, as " + aside.asideName)
	}

	if aside.wasRunning {
		if err := swapper.Start(aside.id); err != nil {
			utils.Error("EditContainer - Failed to start "+aside.name+" again", err)
			return errors.New("The previous container is back but could not be started: " + err.Error())
		}
	}

	aside.settled = true
	utils.Log("EditContainer - Container " + aside.name + " put back as it was")
	return nil
}

// revert is putBack for EditContainer: it returns what EditContainer returns
// for a failed edit, always with the reason the edit failed.
func (aside *asideContainer) revert(replacementID string, createError error, runError error) (string, error) {
	cause := createError
	if cause == nil {
		cause = runError
	}
	reason := "unknown error"
	if cause != nil {
		reason = cause.Error()
	}

	if aside.settled {
		return "", errors.New("Failed to edit container: " + reason)
	}
	if err := aside.putBack(replacementID); err != nil {
		return "", errors.New("Failed to edit container: " + reason + ". " + err.Error())
	}
	return aside.id, errors.New("Failed to edit container, but restored to previous state. Error was: " + reason)
}

// drop removes the container that was set aside, once its replacement runs.
// A failure is not the edit's failure: the new container is in place.
func (aside *asideContainer) drop() {
	if aside == nil || aside.settled {
		return
	}
	aside.settled = true
	if err := swapper.Remove(aside.id, false); err != nil && !containerGone(err) {
		utils.Error("EditContainer - The change is applied, but the previous container could not be removed. It is left, stopped, as "+aside.asideName, err)
	}
}

// rollbackPutBack is the "restore" step of a service rollback for a container
// that was set aside (api_blueprint.go).
func rollbackPutBack(action DockerServiceCreateRollback, OnLog func(string)) {
	aside := action.aside
	utils.Log("Restoring container " + aside.name + "...")

	// by now the name belongs to the replacement, if there is one
	if err := aside.putBack(swapper.IDByName(aside.name)); err != nil {
		utils.Error("Rollback: Container", err)
		OnLog(utils.DoErr("Rollback: Container %s", err))
		return
	}
	utils.Log("Rolled back container " + aside.name)
	OnLog("Rolled back container " + aside.name + "\n")
}

// dropAsides removes the containers a service update set aside, once the
// service is up.
func dropAsides(actions []DockerServiceCreateRollback) {
	for _, action := range actions {
		action.aside.drop()
	}
}
