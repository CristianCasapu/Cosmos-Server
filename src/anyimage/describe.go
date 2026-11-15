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
	"net/url"
	"sort"
	"strings"
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
	// HubLeft: with "hubReserve", how many pulls Docker Hub said were left
	HubLeft int `json:"hubLeft,omitempty"`
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

// The caches. Every key carries the registry and the repository, and the
// manifests and configurations are stored under the digest of their own
// body, computed here: a registry can never make one image's answer be
// served for another (plan 40, security review F1). A description taken
// from the cache gets its identity (registry, repository, page …) from the
// reference asked for, never from the cache.
var (
	descriptions = newByteCache(cacheBytes) // 30 minutes (2 for a failure)
	blobs        = newByteCache(cacheBytes) // manifests and configurations by digest, 24 hours
	platformsOf  = newByteCache(1 << 20)    // the platforms of a reference, for the processor check (KnownPlatforms)
)

func blobKey(ref Reference, digest string) string {
	return ref.Registry + "|" + ref.Repository + "|" + digest
}

// identity sets what a description says about which image it is, from the reference.
func identity(desc Description, ref Reference) Description {
	desc.Reference, desc.Registry, desc.Repository, desc.Tag = ref.String(), ref.Registry, ref.Repository, ref.Tag
	desc.Official = ref.Registry == hubRegistry && strings.HasPrefix(ref.Repository, "library/")
	desc.Page = ref.Page()
	return desc
}

// reader reads one repository for one user, counting what Docker Hub counts.
type reader struct {
	client *registryClient
	ref    Reference
	user   string
}

func (r *reader) hub() bool { return r.ref.Registry == hubRegistry }

// manifest answers the manifest at a tag or digest. Known digests come from
// the cache; a counted GET of Docker Hub needs the allowance; the body must
// hash to the digest the registry announced (HEAD) or was asked for.
func (r *reader) manifest(ctx context.Context, tagOrDigest string) ([]byte, string, string, error) {
	want := ""
	if digestPattern.MatchString(tagOrDigest) {
		want = tagOrDigest
	} else {
		// a HEAD is not counted as a pull: the digest first
		res, _, err := r.client.do(ctx, http.MethodHead, "/v2/"+r.ref.Repository+"/manifests/"+tagOrDigest, manifestTypes, 0)
		if err != nil && (err == errNotFound || err == errUnauthorized || err == ErrInternal || err == errRateLimited) {
			return nil, "", "", err
		}
		if err == nil {
			if digest := res.Header.Get("Docker-Content-Digest"); digestPattern.MatchString(digest) {
				want = digest
			}
		}
	}
	if want != "" {
		if item, ok := blobs.get(blobKey(r.ref, want), 24*time.Hour); ok {
			return item.data, want, "", nil
		}
	}
	if r.hub() {
		if err := hubBudget.take(r.user, time.Now()); err != nil {
			return nil, "", "", err
		}
	}
	res, data, err := r.client.do(ctx, http.MethodGet, "/v2/"+r.ref.Repository+"/manifests/"+tagOrDigest, manifestTypes, manifestLimit)
	if err != nil {
		return nil, "", "", err
	}
	got := digestOf(data)
	if want != "" && got != want {
		return nil, "", "", errNotImage // the registry's body is not what it announced
	}
	if header := res.Header.Get("Docker-Content-Digest"); header != "" && header != got {
		return nil, "", "", errNotImage
	}
	blobs.put(blobKey(r.ref, got), cacheItem{data: data, size: len(data) + 128})
	return data, got, strings.Split(res.Header.Get("Content-Type"), ";")[0], nil
}

// config answers the configuration blob, checked against its digest.
func (r *reader) config(ctx context.Context, digest string) ([]byte, error) {
	if item, ok := blobs.get(blobKey(r.ref, digest), 24*time.Hour); ok {
		return item.data, nil
	}
	_, data, err := r.client.do(ctx, http.MethodGet, "/v2/"+r.ref.Repository+"/blobs/"+digest, nil, configLimit)
	if err != nil {
		return nil, err
	}
	if digestOf(data) != digest {
		return nil, errNotImage
	}
	blobs.put(blobKey(r.ref, digest), cacheItem{data: data, size: len(data) + 128})
	return data, nil
}

type hubTag struct {
	Digest string `json:"digest"`
	Images []struct {
		Architecture string  `json:"architecture"`
		OS           string  `json:"os"`
		Variant      *string `json:"variant"`
		Digest       string  `json:"digest"`
		Size         int64   `json:"size"`
	} `json:"images"`
}

// hubTagInfo asks Docker Hub's API for a tag's platforms and manifest
// digests (not a pull). false when the API did not answer usefully.
func hubTagInfo(ctx context.Context, ref Reference) (hubTag, error) {
	parts := strings.SplitN(ref.Repository, "/", 2)
	var tag hubTag
	if len(parts) != 2 {
		return tag, errUnreachable
	}
	err := getJSON(ctx, hubAPI+"/v2/namespaces/"+url.PathEscape(parts[0])+"/repositories/"+url.PathEscape(parts[1])+"/tags/"+url.PathEscape(ref.Tag), 512<<10, &tag)
	return tag, err
}

func platformName(os, arch, variant string) string {
	name := os + "/" + arch
	if variant != "" {
		name += "/" + variant
	}
	return clean(name, 64)
}

// describeImage reads the declaration of ref for the platform, for a user.
func describeImage(ctx context.Context, ref Reference, platform Platform, user string) Description {
	key := ref.Registry + "|" + ref.Repository + "|" + ref.Tag + "|" + ref.Digest + "|" + platform.OS + "/" + platform.Arch
	if item, ok := descriptions.get(key, 30*time.Minute); ok && item.desc != nil && (item.desc.Problem == "" || time.Since(item.at) < 2*time.Minute) {
		return identity(*item.desc, ref)
	}
	desc := identity(Description{Platforms: []string{}, Fits: "yes"}, ref)
	finish := func(d Description) Description {
		if d.Problem == "hubReserve" {
			d.HubLeft = hubBudget.left()
		}
		size := 2048 + len(d.Platforms)*64
		if d.Declared != nil {
			raw, _ := json.Marshal(d.Declared)
			size += len(raw)
		}
		if d.Problem != "hubBudget" && d.Problem != "hubReserve" && d.Problem != "busy" {
			copied := d
			descriptions.put(key, cacheItem{desc: &copied, size: size})
		}
		if d.Problem == "" {
			raw, _ := json.Marshal(d.Platforms)
			platformsOf.put(ref.String(), cacheItem{data: raw, size: len(raw) + 64})
		}
		return d
	}
	fail := func(err error) Description {
		desc.Problem = codeOf(err)
		return finish(desc)
	}
	r := &reader{client: &registryClient{ref: ref}, ref: ref, user: user}

	platformDigest := ""
	if r.hub() && ref.Digest == "" {
		// Docker Hub's API lists the platforms and their manifests' digests without a pull
		tag, err := hubTagInfo(ctx, ref)
		if err == errNotFound {
			return fail(errNotFound)
		}
		if err == nil && len(tag.Images) > 0 {
			for i, image := range tag.Images {
				if i >= 64 || image.OS == "" || image.OS == "unknown" || image.Architecture == "unknown" || !digestPattern.MatchString(image.Digest) {
					continue
				}
				variant := ""
				if image.Variant != nil {
					variant = *image.Variant
				}
				name := platformName(image.OS, image.Architecture, variant)
				desc.Platforms = append(desc.Platforms, name)
				if platformDigest == "" && image.OS == platform.OS && normalArch(image.Architecture) == platform.Arch {
					platformDigest, desc.Platform = image.Digest, name
					if image.Size > 0 && image.Size < 1<<40 {
						desc.Size = image.Size
					}
				}
			}
			sort.Strings(desc.Platforms)
			if platformDigest == "" && len(desc.Platforms) > 0 {
				desc.Fits = "no"
				return finish(desc)
			}
		}
	}

	var doc manifestDoc
	if platformDigest == "" {
		target := ref.Tag
		if ref.Digest != "" {
			target = ref.Digest
		}
		data, _, mediaType, err := r.manifest(ctx, target)
		if err != nil {
			return fail(err)
		}
		if json.Unmarshal(data, &doc) != nil {
			return fail(errNotImage)
		}
		if doc.MediaType != "" {
			mediaType = doc.MediaType
		}
		if len(doc.Manifests) > 0 {
			desc.Platforms = []string{}
			for i, m := range doc.Manifests {
				if i >= 64 || m.Platform == nil || m.Platform.OS == "" || m.Platform.OS == "unknown" || m.Platform.Architecture == "unknown" {
					continue // attestations
				}
				name := platformName(m.Platform.OS, m.Platform.Architecture, m.Platform.Variant)
				desc.Platforms = append(desc.Platforms, name)
				if platformDigest == "" && m.Platform.OS == platform.OS && normalArch(m.Platform.Architecture) == platform.Arch && digestPattern.MatchString(m.Digest) {
					platformDigest, desc.Platform = m.Digest, name
				}
			}
			sort.Strings(desc.Platforms)
			if platformDigest == "" {
				desc.Fits = "no"
				return finish(desc)
			}
		} else if strings.Contains(mediaType, "index") || strings.Contains(mediaType, "list") {
			return fail(errNotImage)
		}
	}
	if platformDigest != "" {
		data, _, _, err := r.manifest(ctx, platformDigest)
		if err != nil {
			return fail(err)
		}
		doc = manifestDoc{}
		if json.Unmarshal(data, &doc) != nil {
			return fail(errNotImage)
		}
	}
	if doc.Config == nil || !configTypes[doc.Config.MediaType] || !digestPattern.MatchString(doc.Config.Digest) {
		return fail(errNotImage)
	}
	if doc.Config.Size > configLimit {
		return fail(errTooLarge)
	}
	data, err := r.config(ctx, doc.Config.Digest)
	if err != nil {
		return fail(err)
	}
	var config configDoc
	if json.Unmarshal(data, &config) != nil {
		return fail(errNotImage)
	}
	if len(desc.Platforms) == 0 && config.OS != "" {
		desc.Platforms = []string{platformName(config.OS, config.Architecture, "")}
		desc.Platform = desc.Platforms[0]
		if config.OS != platform.OS || normalArch(config.Architecture) != platform.Arch {
			desc.Fits = "no"
		}
	}
	desc.Declared = declaredFrom(config)
	if desc.Size == 0 {
		for i, layer := range doc.Layers {
			if i < 512 && layer.Size > 0 && layer.Size < 1<<40 {
				desc.Size += layer.Size
			}
		}
	}
	return finish(desc)
}

// KnownPlatforms answers the platforms of an image Cosmos described in the
// last half hour, so that the processor check of the review and of the
// creation (docker/fork_image_arch.go) does not read the registry again
// (each read of a Docker Hub manifest counts as a pull).
func KnownPlatforms(image string) ([]string, bool) {
	ref, err := Parse(strings.TrimSpace(image), nil)
	if err != nil {
		return nil, false
	}
	item, ok := platformsOf.get(ref.String(), 30*time.Minute)
	if !ok {
		return nil, false
	}
	var list []string
	if json.Unmarshal(item.data, &list) != nil || len(list) == 0 {
		return nil, false
	}
	return list, true
}

func codeOf(err error) string {
	switch e := err.(type) {
	case nil:
		return ""
	case errHubLimit:
		return e.code
	}
	switch err {
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
