package docker

// Fork addition (plan 43): why a container does not start, in words.
//
// StartReport reads a container's state and the end of its log (bounded:
// at most startLogBytes read, startLogLines kept, each line cut), removes
// colours and control characters, replaces the values of its secret
// variables by ***, and recognises the usual sentences with which images
// say that a setting is missing ("The X environment variable is empty or
// not set", "You must specify X", "You need to specify one of X, Y", "X is
// required"...). What it recognises becomes the repair the panel offers:
// set those variables (a generated value for a secret) and create the
// container again, through the same checks as any edit. A switch that
// allows an empty password is never proposed.
//
// The log is untrusted text: it is answered as data, never as HTML, and
// only to users who may read logs (the logs route's permission).

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

const (
	startLogBytes = 64 << 10
	startLogLines = 40
	startLineMax  = 400
	startNeedsMax = 8
)

// StartNeed is a setting the image's log says is missing.
type StartNeed struct {
	Name   string `json:"name"`
	Secret bool   `json:"secret"`
	// Line: the sentence it was read from (cleaned, secrets masked)
	Line string `json:"line"`
}

// StartReport is what is known about a container that does not run as it should.
type StartReport struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	State        string `json:"state"` // running, restarting, exited, created, paused, dead, missing
	Running      bool   `json:"running"`
	Restarting   bool   `json:"restarting"`
	RestartCount int    `json:"restartCount"`
	ExitCode     int    `json:"exitCode"`
	OOMKilled    bool   `json:"oomKilled"`
	Health       string `json:"health,omitempty"`
	StartedAt    string `json:"startedAt,omitempty"`
	FinishedAt   string `json:"finishedAt,omitempty"`
	// Failing: the container keeps stopping, or stopped with an error
	Failing bool `json:"failing"`
	// Reason: missingSetting, permission, processor, memory, exited, restarting, unhealthy, ""
	Reason string      `json:"reason"`
	Needs  []StartNeed `json:"needs"`
	Log    []string    `json:"log"`
	// LogCut: the log had more than what is shown
	LogCut bool `json:"logCut"`
}

var (
	ansiPattern = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)
	envName     = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{1,63}$`)
	secretName  = regexp.MustCompile(`(?i)(PASS(WORD)?|PASSWD|SECRET|TOKEN|(^|_)KEY$|PRIVATE|CREDENTIALS?)(_|$)`)
	// switches that make a missing secret optional: never the repair
	allowEmptyName = regexp.MustCompile(`(?i)(ALLOW_EMPTY|RANDOM_ROOT_PASSWORD|HOST_AUTH_METHOD|_HASH$|_FILE$|^ALLOW_)`)
	notVariables   = map[string]bool{"ERROR": true, "WARN": true, "WARNING": true, "INFO": true, "DEBUG": true, "FATAL": true, "NOTE": true, "NOT": true, "THE": true, "AND": true, "YES": true}

	name = `([A-Z][A-Z0-9_]{1,63})`
	// each finds the variable in its first group
	needPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i:the )` + name + `(?i: environment variable is (?:empty or )?not set)`),
		regexp.MustCompile(`(?i:you must (?:specify|set|provide|define) )` + name + `\b`),
		regexp.MustCompile(`(?i:please (?:specify|set|provide|define) (?:the )?)` + name + `\b`),
		regexp.MustCompile(`(?i:set the )` + name + `(?i: (?:environment )?variable)`),
		regexp.MustCompile(`\b` + name + `(?i: (?:environment variable |env var |variable |setting )?(?:is |must be |has to be )?(?:required|mandatory|not set|not defined|missing|empty|unset|must be set|must be defined|cannot be empty|must not be empty|needs to be set))\b`),
		regexp.MustCompile(`(?i:(?:missing|required|undefined|unset) (?:required )?(?:environment variable|env var|variable|setting)s?:?\s+['"]?)` + name + `\b`),
	}
	// "You need to specify one of A, B and C": the first that is a value, not a switch
	oneOfPattern      = regexp.MustCompile(`(?i)(?:need to|must) (?:specify|set|provide) one of(?: the following)?(?: as an environment variable)?:?\s*(.*)$`)
	listedName        = regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b`)
	permissionPattern = regexp.MustCompile(`(?i)(permission denied|operation not permitted|cannot create directory|not writable|read-only file system)`)
	processorPattern  = regexp.MustCompile(`(?i)(illegal instruction|requires a cpu with avx|avx support|exec format error)`)
)

// cleanLogLine removes colours and control characters and bounds a line.
func cleanLogLine(line string) string {
	line = ansiPattern.ReplaceAllString(line, "")
	line = strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, line)
	line = strings.TrimRight(line, " ")
	if len(line) > startLineMax {
		cut := startLineMax
		for cut > 0 && !utf8Start(line[cut]) {
			cut--
		}
		line = line[:cut] + "…"
	}
	return line
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// secretValues are the values the log must not show: those of the
// container's secret variables (and of the ones the request sets).
func secretValues(env []string) []string {
	out := []string{}
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 && secretName.MatchString(parts[0]) && !strings.HasSuffix(strings.ToUpper(parts[0]), "_FILE") && len(parts[1]) >= 4 {
			out = append(out, parts[1])
		}
	}
	// longest first, so that a value containing another is masked whole
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func maskSecrets(line string, secrets []string) string {
	for _, s := range secrets {
		line = strings.ReplaceAll(line, s, "***")
	}
	return line
}

// splitDockerLog reads Docker's log stream (multiplexed unless the
// container has a terminal) in order, into lines.
func splitDockerLog(data []byte, tty bool) []string {
	text := data
	if !tty {
		var out bytes.Buffer
		rest := data
		for len(rest) >= 8 {
			size := int(binary.BigEndian.Uint32(rest[4:8]))
			if rest[0] > 2 || rest[1] != 0 || rest[2] != 0 || rest[3] != 0 {
				// not a multiplexed stream after all
				out.Reset()
				out.Write(data)
				rest = nil
				break
			}
			rest = rest[8:]
			if size > len(rest) {
				size = len(rest)
			}
			out.Write(rest[:size])
			rest = rest[size:]
		}
		text = out.Bytes()
	}
	lines := strings.Split(strings.ReplaceAll(string(text), "\r\n", "\n"), "\n")
	out := []string{}
	for _, l := range lines {
		for _, part := range strings.Split(l, "\r") {
			if c := cleanLogLine(part); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// recogniseNeeds finds the settings the log says are missing, in the
// order they are said, each once.
func recogniseNeeds(lines []string, have map[string]string) []StartNeed {
	needs := []StartNeed{}
	seen := map[string]bool{}
	add := func(n string, line string) {
		if seen[n] || len(needs) >= startNeedsMax || notVariables[n] || !envName.MatchString(n) || allowEmptyName.MatchString(n) {
			return
		}
		// a variable that is set and not empty is not missing
		if v, ok := have[n]; ok && v != "" {
			return
		}
		seen[n] = true
		needs = append(needs, StartNeed{Name: n, Secret: secretName.MatchString(n), Line: line})
	}
	for i, line := range lines {
		if m := oneOfPattern.FindStringSubmatch(line); m != nil {
			// the names are on this line or listed on the next ones ("- NAME")
			listed := listedName.FindAllString(m[1], -1)
			for j := i + 1; j < len(lines) && j <= i+6; j++ {
				next := strings.TrimSpace(lines[j])
				if len(listed) > 0 && !strings.HasPrefix(next, "-") {
					break
				}
				listed = append(listed, listedName.FindAllString(next, -1)...)
			}
			for _, n := range listed {
				if !allowEmptyName.MatchString(n) {
					add(n, line)
					break
				}
			}
			continue
		}
		for _, p := range needPatterns {
			for _, m := range p.FindAllStringSubmatch(line, -1) {
				add(m[1], line)
			}
		}
	}
	return needs
}

// reasonOf says in one word what went wrong.
func reasonOf(r *StartReport) string {
	if len(r.Needs) > 0 {
		return "missingSetting"
	}
	if r.OOMKilled || r.ExitCode == 137 && !r.Running {
		return "memory"
	}
	if r.ExitCode == 132 {
		return "processor"
	}
	for _, l := range r.Log {
		if processorPattern.MatchString(l) {
			return "processor"
		}
	}
	for _, l := range r.Log {
		if permissionPattern.MatchString(l) {
			return "permission"
		}
	}
	if r.Health == "unhealthy" {
		return "unhealthy"
	}
	if r.Restarting || r.RestartCount > 0 {
		return "restarting"
	}
	if r.Failing {
		return "exited"
	}
	return ""
}

// reportFrom fills a report from an inspected container and its log.
func reportFrom(c types.ContainerJSON, logData []byte, cut bool, extraSecrets []string) StartReport {
	r := StartReport{Needs: []StartNeed{}, Log: []string{}}
	if c.ContainerJSONBase == nil || c.State == nil {
		r.State = "missing"
		return r
	}
	r.Name = strings.TrimPrefix(c.Name, "/")
	r.State = c.State.Status
	r.Running = c.State.Running
	r.Restarting = c.State.Restarting
	r.RestartCount = c.RestartCount
	r.ExitCode = c.State.ExitCode
	r.OOMKilled = c.State.OOMKilled
	r.StartedAt = c.State.StartedAt
	r.FinishedAt = c.State.FinishedAt
	if c.State.Health != nil {
		r.Health = c.State.Health.Status
	}
	env := []string{}
	tty := false
	if c.Config != nil {
		r.Image = c.Config.Image
		env = c.Config.Env
		tty = c.Config.Tty
	}
	secrets := secretValues(append(append([]string{}, env...), extraSecrets...))
	lines := splitDockerLog(logData, tty)
	if len(lines) > startLogLines {
		lines, cut = lines[len(lines)-startLogLines:], true
	}
	for i := range lines {
		lines[i] = maskSecrets(lines[i], secrets)
	}
	r.Log, r.LogCut = lines, cut
	have := map[string]string{}
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			have[parts[0]] = parts[1]
		} else {
			have[parts[0]] = ""
		}
	}
	r.Failing = r.Restarting || (!r.Running && r.ExitCode != 0) || r.Health == "unhealthy" ||
		(r.Running && r.RestartCount > 0 && recentlyStarted(r.StartedAt, 2*time.Minute))
	if r.Failing {
		r.Needs = recogniseNeeds(lines, have)
	}
	r.Reason = reasonOf(&r)
	return r
}

func recentlyStarted(at string, within time.Duration) bool {
	t, err := time.Parse(time.RFC3339Nano, at)
	return err == nil && time.Since(t) < within
}

// readStartLog is a variable so that tests answer for Docker.
var readStartLog = func(ctx context.Context, name string) ([]byte, bool, error) {
	reader, err := DockerClient.ContainerLogs(ctx, name, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "200"})
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, startLogBytes+1))
	if err != nil && len(data) == 0 {
		return nil, false, err
	}
	cut := len(data) > startLogBytes
	if cut {
		// keep the end: that is where the reason is
		data = data[len(data)-startLogBytes:]
	}
	return data, cut, nil
}

// StartReportOf reads the report of one container.
func StartReportOf(ctx context.Context, name string, extraSecrets []string) (StartReport, error) {
	if err := Connect(); err != nil {
		return StartReport{}, err
	}
	c, err := DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		return StartReport{Name: name, State: "missing", Needs: []StartNeed{}, Log: []string{}}, err
	}
	data, cut, _ := readStartLog(ctx, c.ID)
	return reportFrom(c, data, cut, extraSecrets), nil
}
