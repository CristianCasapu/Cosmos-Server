package docker

// Fork addition: compose values that come in more than one shape.
//
// Upstream decoded `command` and `entrypoint` as one string split on spaces
// (an exec-form list made the whole request fail, a quoted argument with a
// space was cut in two), healthcheck durations as whole seconds (compose
// writes "1m30s") and the healthcheck test as a list only. These types take
// every form the compose specification allows, and the service gets the
// keys upstream dropped without a word: shm_size, tmpfs, init, read_only.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	conttype "github.com/docker/docker/api/types/container"
	"github.com/docker/go-units"
)

// forkStringList is a compose value given as a string or as a list of
// strings. A string is split into words like a shell would (quotes and
// backslashes respected), without running a shell.
type forkStringList struct {
	list  []string
	given bool
	// fromString tells that the value was one string (shell-split), not a list
	fromString bool
	raw        string
}

func (s *forkStringList) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*s = forkStringList{}
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		words, err := shellWords(text)
		if err != nil {
			return err
		}
		*s = forkStringList{list: words, given: text != "", fromString: true, raw: text}
		return nil
	}
	var items []interface{}
	if err := json.Unmarshal(data, &items); err != nil {
		return errors.New("expected a text or a list of texts")
	}
	list := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case string:
			list = append(list, v)
		case float64:
			list = append(list, strconv.FormatFloat(v, 'f', -1, 64))
		case bool:
			list = append(list, strconv.FormatBool(v))
		default:
			return errors.New("expected a list of texts")
		}
	}
	*s = forkStringList{list: list, given: len(list) > 0}
	return nil
}

func (s forkStringList) MarshalJSON() ([]byte, error) {
	if s.fromString {
		return json.Marshal(s.raw)
	}
	if s.list == nil {
		return []byte("null"), nil
	}
	return json.Marshal(s.list)
}

// Given tells whether the value was set (and not empty).
// MarshalYAML: the same value as MarshalJSON, for the YAML export of Cosmos'
// own container (export.go); without it the struct's fields are unexported
// and the value would be lost.
func (s forkStringList) MarshalYAML() (interface{}, error) {
	if s.fromString {
		return s.raw, nil
	}
	if s.list == nil {
		return nil, nil
	}
	return s.list, nil
}

func (s forkStringList) Given() bool { return s.given }

// Args returns the words.
func (s forkStringList) Args() []string { return append([]string(nil), s.list...) }

// shellWords splits a command line into words the way a POSIX shell does for
// quoting (single quotes, double quotes, backslashes), without expanding
// anything and without running a shell.
func shellWords(line string) ([]string, error) {
	words := []string{}
	var current strings.Builder
	inWord := false
	quote := rune(0)
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			inWord = true
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case quote == '"':
			if r == '"' {
				quote = 0
			} else if r == '\\' {
				escaped = true
			} else {
				current.WriteRune(r)
			}
		case r == '\\':
			escaped = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, errors.New("a quote is not closed")
	}
	if escaped {
		current.WriteRune('\\')
	}
	if inWord {
		words = append(words, current.String())
	}
	return words, nil
}

// forkDuration is a compose duration: a number of seconds (what upstream's
// form sends) or a text such as "1m30s", "10s", "500ms", "1h".
type forkDuration time.Duration

func (d *forkDuration) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" || trimmed == `""` {
		*d = 0
		return nil
	}
	var seconds float64
	if err := json.Unmarshal(data, &seconds); err == nil {
		if seconds < 0 {
			return errors.New("a duration cannot be negative")
		}
		*d = forkDuration(time.Duration(seconds * float64(time.Second)))
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("expected a duration such as 30s or 1m30s")
	}
	parsed, err := parseComposeDuration(text)
	if err != nil {
		return err
	}
	*d = forkDuration(parsed)
	return nil
}

func (d forkDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d forkDuration) duration() time.Duration { return time.Duration(d) }

// parseComposeDuration reads "1m30s", "90s", "1h", "500ms", "2d" or a bare
// number of seconds.
func parseComposeDuration(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	if seconds, err := strconv.ParseFloat(text, 64); err == nil {
		if seconds < 0 {
			return 0, errors.New("a duration cannot be negative")
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	// Go does not know days
	if strings.HasSuffix(text, "d") {
		if days, err := strconv.ParseFloat(strings.TrimSuffix(text, "d"), 64); err == nil && days >= 0 {
			return time.Duration(days * 24 * float64(time.Hour)), nil
		}
	}
	parsed, err := time.ParseDuration(text)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("%q is not a duration such as 30s or 1m30s", text)
	}
	return parsed, nil
}

// forkHealthTest is a healthcheck test: a list (["CMD", ...],
// ["CMD-SHELL", "..."], ["NONE"]) or a text, which compose runs as
// CMD-SHELL.
type forkHealthTest []string

func (h *forkHealthTest) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*h = nil
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			*h = nil
		} else {
			*h = forkHealthTest{"CMD-SHELL", text}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return errors.New("a healthcheck test is a text or a list of texts")
	}
	*h = forkHealthTest(list)
	return nil
}

// forkBytes is a size in bytes given as a number or as a text such as "64m".
type forkBytes string

func (b *forkBytes) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "null" {
		*b = ""
		return nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		*b = forkBytes(strconv.FormatInt(int64(number), 10))
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return errors.New("expected a size such as 64m or 1g")
	}
	*b = forkBytes(strings.TrimSpace(text))
	return nil
}

// bytes reads the size; 0 when it is empty.
func (b forkBytes) bytes() (int64, error) {
	if b == "" {
		return 0, nil
	}
	size, err := units.RAMInBytes(string(b))
	if err != nil || size < 0 {
		return 0, fmt.Errorf("%q is not a size such as 64m or 1g", string(b))
	}
	return size, nil
}

// forkServiceExtras are the compose keys of a service that upstream did not
// read. Embedded in ContainerCreateRequestContainer, so they sit at the same
// level as the other keys of a service.
type forkServiceExtras struct {
	ShmSize  forkBytes      `json:"shm_size,omitempty"`
	Tmpfs    forkStringList `json:"tmpfs,omitempty"`
	Init     *bool          `json:"init,omitempty"`
	ReadOnly bool           `json:"read_only,omitempty"`
}

// applyServiceExtras writes them into what Docker is given. Values were
// checked by CheckServiceRequest before anything was created.
func applyServiceExtras(container ContainerCreateRequestContainer, hostConfig *conttype.HostConfig) error {
	if container.ShmSize != "" {
		size, err := container.ShmSize.bytes()
		if err != nil {
			return errors.New("shm_size: " + err.Error())
		}
		hostConfig.ShmSize = size
	}
	if container.Tmpfs.Given() {
		if hostConfig.Tmpfs == nil {
			hostConfig.Tmpfs = map[string]string{}
		}
		for _, entry := range container.Tmpfs.list {
			path, options, _ := strings.Cut(entry, ":")
			hostConfig.Tmpfs[path] = options
		}
	}
	if container.Init != nil {
		value := *container.Init
		hostConfig.Init = &value
	}
	hostConfig.ReadonlyRootfs = container.ReadOnly
	return nil
}

// restartPolicy reads compose's restart value: "no", "always",
// "unless-stopped", "on-failure" or "on-failure:5".
func restartPolicy(value string) conttype.RestartPolicy {
	name, retries, found := strings.Cut(strings.TrimSpace(value), ":")
	policy := conttype.RestartPolicy{Name: conttype.RestartPolicyMode(name)}
	if found && name == "on-failure" {
		if count, err := strconv.Atoi(retries); err == nil && count > 0 {
			policy.MaximumRetryCount = count
		}
	}
	return policy
}

// healthConfig turns the healthcheck of a service into Docker's, nil when
// there is none. disable (or a test of NONE) switches off the one of the image.
func healthConfig(check ContainerCreateRequestContainerHealthcheck) *conttype.HealthConfig {
	if check.Disable {
		return &conttype.HealthConfig{Test: []string{"NONE"}}
	}
	if len(check.Test) == 0 {
		return nil
	}
	return &conttype.HealthConfig{
		Test:          []string(check.Test),
		Interval:      check.Interval.duration(),
		Timeout:       check.Timeout.duration(),
		StartPeriod:   check.StartPeriod.duration(),
		StartInterval: check.StartInterval.duration(),
		Retries:       check.Retries,
	}
}

// forkList makes a list value (from `inspect`), nil when it is empty.
func forkList(list []string) forkStringList {
	if len(list) == 0 {
		return forkStringList{}
	}
	return forkStringList{list: append([]string(nil), list...), given: true}
}

// exportServiceExtras reads back what applyServiceExtras wrote, so that the
// edit of a container (which goes through the export) keeps it.
func exportServiceExtras(hostConfig *conttype.HostConfig) forkServiceExtras {
	extras := forkServiceExtras{}
	if hostConfig == nil {
		return extras
	}
	// Docker's default is 64 MB: only a size that differs was asked for
	if hostConfig.ShmSize > 0 && hostConfig.ShmSize != 64*1024*1024 {
		extras.ShmSize = forkBytes(strconv.FormatInt(hostConfig.ShmSize, 10))
	}
	if len(hostConfig.Tmpfs) > 0 {
		paths := make([]string, 0, len(hostConfig.Tmpfs))
		for path, options := range hostConfig.Tmpfs {
			if options != "" {
				path += ":" + options
			}
			paths = append(paths, path)
		}
		sort.Strings(paths)
		extras.Tmpfs = forkList(paths)
	}
	if hostConfig.Init != nil {
		value := *hostConfig.Init
		extras.Init = &value
	}
	extras.ReadOnly = hostConfig.ReadonlyRootfs
	return extras
}

// forkNamesInRequest are the container names of a request, for the start
// order: a network_mode of container:<name> waits only for a container of the
// same request.
func forkNamesInRequest(services map[string]ContainerCreateRequestContainer) map[string]bool {
	names := map[string]bool{}
	for _, service := range services {
		names[service.Name] = true
	}
	return names
}
