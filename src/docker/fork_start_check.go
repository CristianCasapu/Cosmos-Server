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
	startLogBytes = 64 << 10 // the end of the log kept
	startReadMax  = 4 << 20  // at most this much of Docker's stream is read
	maskMinLength = 8        // a value shorter than this is not masked: masking a common word would tell it
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
	secretName  = regexp.MustCompile(`(?i)(PASS(WORD|PHRASE)?|PASSWD|SECRET|TOKEN|(^|_)KEY$|PRIVATE|CREDENTIALS?)(_|$)`)
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
		regexp.MustCompile(`(?i:(?:via|with|using|in) the (?:environment )?variable )` + name + `\b`),
	}
	// "You need to specify one of A, B and C": the first that is a value, not a switch
	oneOfPattern = regexp.MustCompile(`(?i)(?:need to|must) (?:specify|set|provide) one of(?: the following)?(?: as an environment variable)?:?\s*(.*)$`)
	listedName   = regexp.MustCompile(`\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b`)
	// "You need to define the A and B (or B_FILE) environment variables": every one named
	allOfPattern      = regexp.MustCompile(`(?i)(?:need|have) to (?:define|set|provide) (?:the )?(.*)variables?`)
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
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || (r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0xfeff {
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

// urlPassword finds the password of an address with credentials
// (postgres://user:password@host/...).
var urlPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]*:)([^@\s/]+)(@)`)

// secretValues are the values the log must not show: those of the
// container's secret variables (and of the ones the request sets), each
// line of a multi-line value too; values shorter than maskMinLength are not
// masked (masking every "postgres" in a log would tell that the password is
// "postgres").
func secretValues(env []string) []string {
	out := []string{}
	add := func(v string) {
		if len(v) >= maskMinLength {
			out = append(out, v)
		}
	}
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || strings.HasSuffix(strings.ToUpper(parts[0]), "_FILE") {
			continue
		}
		if secretName.MatchString(parts[0]) {
			add(parts[1])
			if strings.ContainsAny(parts[1], "\r\n") {
				for _, line := range strings.FieldsFunc(parts[1], func(r rune) bool { return r == '\n' || r == '\r' }) {
					add(strings.TrimSpace(line))
				}
			}
		}
		// any variable that carries an address with a password (DATABASE_URL=postgres://u:pw@db/x)
		for _, m := range urlPassword.FindAllStringSubmatch(parts[1], -1) {
			add(m[2])
		}
	}
	// longest first, so that a value containing another is masked whole
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// maskText hides the secret values in the whole text, before it is cut into
// lines (a value cut in two would not be found), and the password of every
// address with credentials, whatever its length (only in that place).
func maskText(text string, secrets []string) string {
	text = urlPassword.ReplaceAllString(text, "${1}<hidden>${3}")
	for _, v := range secrets {
		text = strings.ReplaceAll(text, v, "<hidden>")
	}
	return text
}

// demuxTail reads Docker's log stream (frames of stdout and stderr, in
// order, unless the container has a terminal) and keeps the END of the text:
// at most startLogBytes, from at most startReadMax read. A stream that is not
// framed after all is taken as plain text.
func demuxTail(r io.Reader, tty bool) ([]byte, bool) {
	r = io.LimitReader(r, startReadMax)
	keep := []byte{}
	cut := false
	push := func(b []byte) {
		keep = append(keep, b...)
		if len(keep) > 2*startLogBytes {
			keep = append([]byte{}, keep[len(keep)-startLogBytes:]...)
			cut = true
		}
	}
	chunk := make([]byte, 32<<10)
	raw := func() {
		for {
			n, err := r.Read(chunk)
			push(chunk[:n])
			if err != nil {
				return
			}
		}
	}
	if tty {
		raw()
	} else {
		header := make([]byte, 8)
		for {
			if _, err := io.ReadFull(r, header); err != nil {
				break
			}
			if header[0] > 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
				push(header)
				raw()
				break
			}
			size := int64(binary.BigEndian.Uint32(header[4:8]))
			for size > 0 {
				want := int64(len(chunk))
				if size < want {
					want = size
				}
				n, err := io.ReadFull(r, chunk[:want])
				push(chunk[:n])
				size -= int64(n)
				if err != nil {
					size = 0
					break
				}
			}
		}
	}
	if len(keep) > startLogBytes {
		keep, cut = keep[len(keep)-startLogBytes:], true
	}
	if cut {
		// the first line is a piece of a line: dropped
		if i := bytes.IndexByte(keep, '\n'); i >= 0 {
			keep = keep[i+1:]
		}
	}
	return keep, cut
}

// splitLog cuts the (already masked) text into clean lines.
func splitLog(text string) []string {
	out := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		for _, part := range strings.Split(l, "\r") {
			if c := cleanLogLine(part); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// deniedName: a variable never proposed by the repair, whatever a log says:
// what changes how programs are loaded or where they connect, the shell's
// own, and Docker's and Cosmos' own.
var deniedNames = map[string]bool{
	"PATH": true, "HOME": true, "HOSTNAME": true, "USER": true, "SHELL": true, "TERM": true, "PWD": true, "IFS": true,
	"ENV": true, "BASH_ENV": true, "PS4": true, "SHELLOPTS": true, "LANG": true, "LC_ALL": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true, "FTP_PROXY": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "CURL_CA_BUNDLE": true, "REQUESTS_CA_BUNDLE": true, "NODE_EXTRA_CA_CERTS": true,
	"NODE_OPTIONS": true, "JAVA_TOOL_OPTIONS": true, "_JAVA_OPTIONS": true, "JDK_JAVA_OPTIONS": true, "GODEBUG": true, "GOFLAGS": true,
	"PYTHONPATH": true, "PYTHONSTARTUP": true, "PYTHONHOME": true, "PERL5LIB": true, "PERL5OPT": true, "RUBYOPT": true, "RUBYLIB": true,
}

func deniedName(name string) bool {
	return deniedNames[name] || strings.HasPrefix(name, "LD_") || strings.HasPrefix(name, "DOCKER_") || strings.HasPrefix(name, "COSMOS_") ||
		strings.HasPrefix(name, "DYLD_") || strings.HasPrefix(name, "GCONV_")
}

// recogniseNeeds finds the settings the log says are missing, in the
// order they are said, each once.
func recogniseNeeds(lines []string, have map[string]string) []StartNeed {
	needs := []StartNeed{}
	seen := map[string]bool{}
	add := func(n string, line string) {
		if seen[n] || len(needs) >= startNeedsMax || notVariables[n] || !envName.MatchString(n) || allowEmptyName.MatchString(n) || deniedName(n) {
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
		if m := allOfPattern.FindStringSubmatch(line); m != nil {
			for _, n := range listedName.FindAllString(m[1], -1) {
				add(n, line)
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

// reportFrom fills a report from an inspected container and the end of its
// log, as demuxTail gives it.
func reportFrom(c types.ContainerJSON, logText []byte, cut bool, extraSecrets []string) StartReport {
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
	if c.Config != nil {
		r.Image = c.Config.Image
		env = c.Config.Env
	}
	secrets := secretValues(append(append([]string{}, env...), extraSecrets...))
	lines := splitLog(maskText(string(logText), secrets))
	if len(lines) > startLogLines {
		lines, cut = lines[len(lines)-startLogLines:], true
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

// readStartLog is a variable so that tests answer for Docker. It answers
// the end of the log, demultiplexed.
var readStartLog = func(ctx context.Context, name string, tty bool) ([]byte, bool, error) {
	reader, err := DockerClient.ContainerLogs(ctx, name, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "200"})
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	text, cut := demuxTail(reader, tty)
	return text, cut, nil
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
	tty := c.Config != nil && c.Config.Tty
	data, cut, _ := readStartLog(ctx, c.ID, tty)
	return reportFrom(c, data, cut, extraSecrets), nil
}
