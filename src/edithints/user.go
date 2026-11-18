package edithints

import (
	"regexp"
	"strconv"
	"strings"
)

// RunAs is the user a container writes its files as.
type RunAs struct {
	UID int `json:"uid"`
	// GID is -1 when only the user is known
	GID int `json:"gid"`
	// Known: UID (and GID) are numbers; a user given by name is not known here
	Known bool `json:"known"`
	// Source: user (the container's user field), image (the image's USER), puid (PUID/PGID of a
	// linuxserver.io-style image), puidDefault (such an image without PUID: its default 911), root (nothing set)
	Source string `json:"source"`
	// Text is the value as it was given ("1000:1000", "node")
	Text string `json:"text"`
}

// linuxserverImage matches the images that start as root and run the app as PUID/PGID.
var linuxserverImage = regexp.MustCompile(`^((docker\.io|lscr\.io|ghcr\.io)/)?(linuxserver|hotio)/`)

// LinuxserverStyle says whether an image takes PUID/PGID: its name, or a
// recipe that names those variables.
func LinuxserverStyle(image string, recipeEnv []string) bool {
	if linuxserverImage.MatchString(strings.ToLower(strings.TrimSpace(image))) {
		return true
	}
	for _, name := range recipeEnv {
		if name == "PUID" {
			return true
		}
	}
	return false
}

func envValue(env []string, name string) (string, bool) {
	for _, line := range env {
		if key, value, found := strings.Cut(line, "="); found && key == name {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func number(text string) (int, bool) {
	n, err := strconv.Atoi(text)
	return n, err == nil && n >= 0 && n < 1<<31
}

// parseUser reads "1000", "1000:1000", "root" or a name.
func parseUser(text string, source string) RunAs {
	text = strings.TrimSpace(text)
	user := RunAs{Text: text, Source: source, GID: -1}
	name, group, hasGroup := strings.Cut(text, ":")
	if name == "root" {
		name = "0"
	}
	uid, ok := number(name)
	if !ok {
		return user
	}
	user.UID, user.Known = uid, true
	if hasGroup {
		if gid, ok := number(group); ok {
			user.GID = gid
		} else if group == "root" {
			user.GID = 0
		}
	} else if uid == 0 {
		user.GID = 0
	}
	return user
}

// EffectiveUser is the user a container writes its files as. The
// container's user field holds the image's USER when nothing else was set
// (Docker copies it), so imageUser only tells where it came from.
func EffectiveUser(containerUser, imageUser string, env []string, linuxserver bool) RunAs {
	containerUser = strings.TrimSpace(containerUser)
	if linuxserver && (containerUser == "" || containerUser == "root" || containerUser == "0" || containerUser == "0:0") {
		puid, hasUID := envValue(env, "PUID")
		pgid, hasGID := envValue(env, "PGID")
		user := RunAs{UID: 911, GID: 911, Known: true, Source: "puidDefault", Text: "911:911"}
		if hasUID || hasGID {
			user.Source = "puid"
			if n, ok := number(puid); ok {
				user.UID = n
			} else if hasUID {
				user.Known = false
			}
			if n, ok := number(pgid); ok {
				user.GID = n
			} else if hasGID {
				user.Known = false
			}
			user.Text = strconv.Itoa(user.UID) + ":" + strconv.Itoa(user.GID)
		}
		return user
	}
	if containerUser == "" {
		return RunAs{UID: 0, GID: 0, Known: true, Source: "root", Text: ""}
	}
	source := "user"
	if containerUser == strings.TrimSpace(imageUser) {
		source = "image"
	}
	return parseUser(containerUser, source)
}

// CommonOwner is the owner ("uid:gid") most of the folders have, "" when
// none is known. Folders of root are left out when another owner exists.
func CommonOwner(folders []Folder) string {
	count := map[string]int{}
	best, bestCount := "", 0
	for _, f := range folders {
		if f.Owner == "" || f.State != "ok" {
			continue
		}
		count[f.Owner]++
	}
	for owner, n := range count {
		if owner == "0:0" && len(count) > 1 {
			continue
		}
		if n > bestCount || n == bestCount && owner < best {
			best, bestCount = owner, n
		}
	}
	return best
}
