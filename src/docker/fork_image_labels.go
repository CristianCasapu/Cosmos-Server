package docker

// Fork addition: labels that come from an image are not obeyed.
//
// Docker copies an image's LABELs into every container made from it, so the
// labels Cosmos reads from a container are the administrator's and the image
// publisher's mixed together. A few cosmos-* labels make Cosmos act:
//
//   cosmos-force-network-mode   the network mode on every recreate (the host's
//                               network, or another container's)
//   cosmos-auto-update          pull and recreate without anyone asking
//   cosmos-network-name         the network the app (and Cosmos) is joined to
//                               when Cosmos runs in a container
//   cosmos-deployment           an exited container carrying it is removed
//                               by the hourly clean-up
//
// For those, a label counts as the administrator's only when its value is not
// the one the image itself carries for that key. The container's labels are
// where the administrator's choices are kept (installs from the Market or a
// compose file and the panel's switches write them there at creation); the
// image's own labels can be read at any time and do not change for an image
// id. A value the administrator chose that happens to equal the image's is
// taken as the image's: for the network mode nothing is lost (the mode chosen
// is also in the container's host settings, which an image cannot set); for
// automatic updates an image that switches them on itself cannot be switched
// on from the panel, switching off always works. When the image cannot be
// read, the label is not obeyed either.
//
// Before a container is recreated, the labels of that list that came from its
// image are left out of the new container's own labels: Docker adds the new
// image's labels again by itself, and the image's label is never handed back
// as if the administrator had set it.
//
// Obeyed from an image, on purpose: cosmos-lazy and its two durations (the
// app is stopped when idle and started on demand: it affects only that app),
// cosmos-force-network-secured (it only takes a network away), and the labels
// that only show or group things (cosmos-icon, cosmos-stack).

import (
	"sync"

	doctype "github.com/docker/docker/api/types"

	"github.com/azukaar/cosmos-server/src/utils"
)

// labelsNotFromImage are the labels whose image value is never obeyed.
var labelsNotFromImage = []string{
	"cosmos-force-network-mode",
	"cosmos-auto-update",
	"cosmos-network-name",
	DeploymentLabel,
	// plan 45 (src/appsignin): which variables are an app's sign-in, and the database a web manager is for
	"cosmos-fork-signin",
	"cosmos-fork-manager-of",
}

// imageLabelsOf reads the labels an image carries (a variable for the tests).
var imageLabelsOf = func(imageID string) (map[string]string, error) {
	if err := Connect(); err != nil {
		return nil, err
	}
	image, _, err := DockerClient.ImageInspectWithRaw(DockerContext, imageID)
	if err != nil {
		return nil, err
	}
	if image.Config == nil {
		return map[string]string{}, nil
	}
	return image.Config.Labels, nil
}

// An image id names one image for good, so its labels are kept (a few hundred
// ids at most).
const imageLabelCacheSize = 512

var (
	imageLabelCacheMu sync.Mutex
	imageLabelCache   = map[string]map[string]string{}
)

func cachedImageLabels(imageID string) (map[string]string, error) {
	imageLabelCacheMu.Lock()
	labels, ok := imageLabelCache[imageID]
	imageLabelCacheMu.Unlock()
	if ok {
		return labels, nil
	}
	labels, err := imageLabelsOf(imageID)
	if err != nil {
		return nil, err
	}
	copied := make(map[string]string, len(labels))
	for key, value := range labels {
		copied[key] = value
	}
	imageLabelCacheMu.Lock()
	if len(imageLabelCache) >= imageLabelCacheSize {
		imageLabelCache = map[string]map[string]string{}
	}
	imageLabelCache[imageID] = copied
	imageLabelCacheMu.Unlock()
	return copied, nil
}

// labelFromImage tells whether key=value of a container made from imageID
// is the image's own label (or cannot be told apart from it).
func labelFromImage(imageID string, key string, value string) bool {
	if value == "" {
		return false
	}
	if imageID == "" {
		// no image to compare with: nothing was copied from one
		return false
	}
	labels, err := cachedImageLabels(imageID)
	if err != nil {
		utils.Warn("Docker: cannot read the labels of image " + imageID + ", the container's label " + key + " is not obeyed: " + err.Error())
		return true
	}
	imageValue, carries := labels[key]
	return carries && imageValue == value
}

// adminLabel is the value of a label of the list above as the administrator
// set it: "" when it is absent or comes from the image.
func adminLabel(container doctype.ContainerJSON, key string) string {
	if container.Config == nil {
		return ""
	}
	value := container.Config.Labels[key]
	if labelFromImage(imageIDOf(container), key, value) {
		return ""
	}
	return value
}

func imageIDOf(container doctype.ContainerJSON) string {
	if container.ContainerJSONBase == nil {
		return ""
	}
	return container.Image
}

// dropImageLabels removes from a container's configuration, before it is
// recreated, the labels of the list above that came from its image.
func dropImageLabels(container *doctype.ContainerJSON) {
	if container == nil || container.Config == nil || container.Config.Labels == nil {
		return
	}
	imageID := imageIDOf(*container)
	for _, key := range labelsNotFromImage {
		if value, ok := container.Config.Labels[key]; ok && labelFromImage(imageID, key, value) {
			utils.Log("Docker: the label " + key + " of " + container.Name + " comes from its image and is not obeyed")
			delete(container.Config.Labels, key)
		}
	}
}
