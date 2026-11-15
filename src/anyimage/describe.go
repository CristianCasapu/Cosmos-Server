package anyimage

// What an image declares, read from its registry without a pull: the
// manifest (an index lists the platforms; the server's own is picked), then
// the image's configuration blob. Nothing of it is run or obeyed: it is shown
// to the administrator and used for proposals.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	manifestLimit = 1 << 20
	configLimit   = 1 << 20
)

var manifestTypes = []string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

var configTypes = map[string]bool{
	"application/vnd.oci.image.config.v1+json":       true,
	"application/vnd.docker.container.image.v1+json": true,
}

// Platform is the server's: os and architecture as registries name them.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

// Healthcheck as the image declares it.
type Healthcheck struct {
	Test     []string `json:"test"`
	Interval int64    `json:"interval,omitempty"` // nanoseconds, as the image config keeps them
	Timeout  int64    `json:"timeout,omitempty"`
	Retries  int      `json:"retries,omitempty"`
}

// Declared is what an image says about itself, bounded.
type Declared struct {
	Ports       []string          `json:"ports"`
	Volumes     []string          `json:"volumes"`
	Env         []string          `json:"env"`
	User        string            `json:"user"`
	Entrypoint  []string          `json:"entrypoint"`
	Cmd         []string          `json:"cmd"`
	Healthcheck *Healthcheck      `json:"healthcheck,omitempty"`
	Labels      map[string]string `json:"labels"`
	WorkingDir  string            `json:"workingDir"`
	StopSignal  string            `json:"stopSignal"`
}

// Description is the answer for one image.
type Description struct {
	Reference  string   `json:"reference"`
	Registry   string   `json:"registry"`
	Repository string   `json:"repository"`
	Tag        string   `json:"tag"`
	Official   bool     `json:"official"`
	Page       string   `json:"page"`
	Platforms  []string `json:"platforms"`
	Platform   string   `json:"platform"`
	// Fits: "yes", "no" (made for other processors only)
	Fits     string    `json:"fits"`
	Declared *Declared `json:"declared,omitempty"`
	Tags     TagList   `json:"tags"`
	Recipe   *Recipe   `json:"recipe,omitempty"`
	// Problem: "" or a code (busy: Cosmos' own bound; invalid, internal, notFound, unauthorized, rateLimited, unreachable, tooLarge, notImage, redirect)
	Problem string `json:"problem,omitempty"`
	// LoggedIn: the server has credentials for this registry (said with "unauthorized")
	LoggedIn bool `json:"loggedIn"`
	// Market: the name of the Market's entry for this image, when a recipe names one
	Market string `json:"market,omitempty"`
	// TagTyped: the administrator named the tag; otherwise Cosmos chose it (Tags.Why)
	TagTyped bool `json:"tagTyped"`
	// Size: what the pull downloads (the compressed layers), 0 when not known
	Size int64 `json:"size"`
	// Databases: the recipes of databases, for "add a database to this app"
	Databases []Recipe `json:"databases"`
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform"`
}

type manifestDoc struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"`
	Config    *descriptor  `json:"config"`
	Layers    []descriptor `json:"layers"`
}

type configDoc struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Config       struct {
		ExposedPorts map[string]json.RawMessage `json:"ExposedPorts"`
		Volumes      map[string]json.RawMessage `json:"Volumes"`
		Env          []string                   `json:"Env"`
		User         string                     `json:"User"`
		Entrypoint   []string                   `json:"Entrypoint"`
		Cmd          []string                   `json:"Cmd"`
		Healthcheck  *struct {
			Test     []string `json:"Test"`
			Interval int64    `json:"Interval"`
			Timeout  int64    `json:"Timeout"`
			Retries  int      `json:"Retries"`
		} `json:"Healthcheck"`
		Labels     map[string]string `json:"Labels"`
		WorkingDir string            `json:"WorkingDir"`
		StopSignal string            `json:"StopSignal"`
	} `json:"config"`
}

// clean bounds a text from an image: no control characters, at most n bytes.
func clean(text string, n int) string {
	out := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	if len(out) > n {
		out = out[:n]
		for len(out) > 0 && !utf8Valid(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }

func cleanList(list []string, count, n int) []string {
	out := []string{}
	for _, item := range list {
		if len(out) >= count {
			break
		}
		out = append(out, clean(item, n))
	}
	return out
}

func sortedKeys(m map[string]json.RawMessage, count int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return cleanList(keys, count, 256)
}

func declaredFrom(doc configDoc) *Declared {
	c := doc.Config
	d := &Declared{
		Ports:      sortedKeys(c.ExposedPorts, 64),
		Volumes:    sortedKeys(c.Volumes, 32),
		Env:        cleanList(c.Env, 128, 512),
		User:       clean(c.User, 128),
		Entrypoint: cleanList(c.Entrypoint, 32, 512),
		Cmd:        cleanList(c.Cmd, 32, 512),
		Labels:     map[string]string{},
		WorkingDir: clean(c.WorkingDir, 256),
		StopSignal: clean(c.StopSignal, 32),
	}
	if c.Healthcheck != nil && len(c.Healthcheck.Test) > 0 {
		d.Healthcheck = &Healthcheck{Test: cleanList(c.Healthcheck.Test, 16, 512), Interval: c.Healthcheck.Interval, Timeout: c.Healthcheck.Timeout, Retries: c.Healthcheck.Retries}
	}
	keys := make([]string, 0, len(c.Labels))
	for key := range c.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if len(d.Labels) >= 64 {
			break
		}
		d.Labels[clean(key, 128)] = clean(c.Labels[key], 512)
	}
	return d
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalArch is the registries' name of an architecture.
func normalArch(arch string) string {
	switch strings.ToLower(arch) {
	case "x86_64", "x86-64", "amd64":
		return "amd64"
	case "aarch64", "arm64", "armv8":
		return "arm64"
	case "armv7l", "armv7", "armhf", "arm":
		return "arm"
	}
	return strings.ToLower(arch)
}

type cached struct {
	at   time.Time
	desc Description
}

var (
	cacheMu     sync.Mutex
	byReference = map[string]cached{} // 30 minutes (2 for a failure)
	byDigest    = map[string]cached{} // six hours
)

func cacheGet(m map[string]cached, key string, keep time.Duration) (Description, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	entry, ok := m[key]
	if !ok {
		return Description{}, false
	}
	if entry.desc.Problem != "" {
		keep = 2 * time.Minute
	}
	if time.Since(entry.at) > keep {
		delete(m, key)
		return Description{}, false
	}
	return entry.desc, true
}

func cachePut(m map[string]cached, key string, desc Description) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(m) > 256 {
		for k := range m {
			delete(m, k)
		}
	}
	m[key] = cached{at: time.Now(), desc: desc}
}

// describeImage reads the manifest and the configuration of ref for the platform.
func describeImage(ctx context.Context, ref Reference, platform Platform) Description {
	desc := Description{
		Reference: ref.String(), Registry: ref.Registry, Repository: ref.Repository, Tag: ref.Tag,
		Official: ref.Registry == hubRegistry && strings.HasPrefix(ref.Repository, "library/"),
		Page:     ref.Page(), Platforms: []string{}, Fits: "yes",
	}
	key := ref.String() + "|" + platform.OS + "/" + platform.Arch
	if cachedDesc, ok := cacheGet(byReference, key, 30*time.Minute); ok {
		return cachedDesc
	}
	fail := func(err error) Description {
		desc.Problem = codeOf(err)
		if desc.Problem == "unauthorized" {
			desc.LoggedIn = loggedIn(ref.Registry)
		}
		cachePut(byReference, key, desc)
		return desc
	}
	client := &registryClient{ref: ref}
	tagOrDigest := ref.Tag
	if ref.Digest != "" {
		tagOrDigest = ref.Digest
	}
	path := "/v2/" + ref.Repository + "/manifests/" + tagOrDigest

	// a HEAD is not counted as a pull by Docker Hub: an unchanged image is answered from the cache
	if res, _, err := client.do(ctx, http.MethodHead, path, manifestTypes, 0); err == nil {
		if digest := res.Header.Get("Docker-Content-Digest"); digestPattern.MatchString(digest) {
			if known, ok := cacheGet(byDigest, digest+"|"+platform.OS+"/"+platform.Arch, 6*time.Hour); ok {
				known.Reference, known.Tag = desc.Reference, desc.Tag
				cachePut(byReference, key, known)
				return known
			}
			defer func() { cachePut(byDigest, digest+"|"+platform.OS+"/"+platform.Arch, desc) }()
		}
	} else if err == errNotFound || err == errUnauthorized || err == ErrInternal {
		return fail(err)
	}

	res, data, err := client.do(ctx, http.MethodGet, path, manifestTypes, manifestLimit)
	if err != nil {
		return fail(err)
	}
	if ref.Digest != "" && digestOf(data) != ref.Digest {
		return fail(errNotImage)
	}
	var doc manifestDoc
	if json.Unmarshal(data, &doc) != nil {
		return fail(errNotImage)
	}
	mediaType := doc.MediaType
	if mediaType == "" {
		mediaType = strings.Split(res.Header.Get("Content-Type"), ";")[0]
	}
	if len(doc.Manifests) > 0 {
		chosen := ""
		for i, m := range doc.Manifests {
			if i >= 64 {
				break
			}
			if m.Platform == nil || m.Platform.OS == "" || m.Platform.OS == "unknown" || m.Platform.Architecture == "unknown" {
				continue // attestations
			}
			name := m.Platform.OS + "/" + m.Platform.Architecture
			if m.Platform.Variant != "" {
				name += "/" + m.Platform.Variant
			}
			desc.Platforms = append(desc.Platforms, clean(name, 64))
			if chosen == "" && m.Platform.OS == platform.OS && normalArch(m.Platform.Architecture) == platform.Arch && digestPattern.MatchString(m.Digest) {
				chosen = m.Digest
				desc.Platform = clean(name, 64)
			}
		}
		sort.Strings(desc.Platforms)
		if chosen == "" {
			desc.Fits = "no"
			cachePut(byReference, key, desc)
			return desc
		}
		_, data, err = client.do(ctx, http.MethodGet, "/v2/"+ref.Repository+"/manifests/"+chosen, manifestTypes, manifestLimit)
		if err != nil {
			return fail(err)
		}
		if digestOf(data) != chosen {
			return fail(errNotImage)
		}
		doc = manifestDoc{}
		if json.Unmarshal(data, &doc) != nil {
			return fail(errNotImage)
		}
	} else if mediaType != "" && strings.Contains(mediaType, "index") {
		return fail(errNotImage)
	}
	if doc.Config == nil || !configTypes[doc.Config.MediaType] || !digestPattern.MatchString(doc.Config.Digest) {
		return fail(errNotImage)
	}
	if doc.Config.Size > configLimit {
		return fail(errTooLarge)
	}
	_, data, err = client.do(ctx, http.MethodGet, "/v2/"+ref.Repository+"/blobs/"+doc.Config.Digest, nil, configLimit)
	if err != nil {
		return fail(err)
	}
	if digestOf(data) != doc.Config.Digest {
		return fail(errNotImage)
	}
	var config configDoc
	if json.Unmarshal(data, &config) != nil {
		return fail(errNotImage)
	}
	if len(desc.Platforms) == 0 && config.OS != "" {
		name := config.OS + "/" + config.Architecture
		desc.Platforms = []string{clean(name, 64)}
		desc.Platform = desc.Platforms[0]
		if config.OS != platform.OS || normalArch(config.Architecture) != platform.Arch {
			desc.Fits = "no"
		}
	}
	desc.Declared = declaredFrom(config)
	for i, layer := range doc.Layers {
		if i < 512 && layer.Size > 0 && layer.Size < 1<<40 {
			desc.Size += layer.Size
		}
	}
	cachePut(byReference, key, desc)
	return desc
}

func codeOf(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalid:
		return "invalid"
	case ErrInternal:
		return "internal"
	case errNotFound, errUnauthorized, errRateLimited, errTooLarge, errNotImage, errBusy:
		return err.Error()
	case errRedirect:
		return "redirect"
	}
	return "unreachable"
}
