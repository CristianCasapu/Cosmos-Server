package anyimage

// What an image needs, from what its publisher wrote about it: the
// description of its repository on Docker Hub (or Quay), read through the
// same guarded client as the tags, never a pull. The text is untrusted: it is
// bounded, stripped of control characters and markup, and only read for
// hints (variable names, which ones the text calls required, example values,
// volumes, ports). Nothing found here is applied: the panel shows it and the
// administrator decides. Example values are hints, never values.
//
// How a description read is counted: it is one call of Docker Hub's API
// (hub.docker.com), like the tag and platform reads, not a pull, so it does
// not take from hubAllowance (which keeps the pull allowance of
// registry-1.docker.io for real pulls). It happens only inside the describe
// route's windows (describePerMinute for the server, describePerUserMinute
// per user), once per repository and 6 hours (the cache below, whatever the
// tag), and a 429 from the API stops every description read for 5 minutes.

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	readmeAnswerLimit = 256 << 10 // the API's JSON answer
	readmeTextLimit   = 128 << 10 // the text that is read (Docker Hub cuts at 25 000 characters)
	readmeMaxLines    = 8000
	readmeLineLimit   = 4 << 10
	readmeJoinLimit   = 16 << 10 // a command continued over lines
	readmeMaxCommands = 64       // docker run examples looked at
	readmeMaxWords    = 256      // words of one command

	maxHintEnv     = 64
	maxHintVolumes = 16
	maxHintPorts   = 16
	maxHintDocs    = 3
	hintExampleLen = 128
	hintAboutLen   = 240

	readmeKeep     = 6 * time.Hour
	readmeKeepFail = 2 * time.Minute
	readmePauseFor = 5 * time.Minute
)

// descriptionsDisabled, when true, stops every description read before it
// starts (no request, no cache). It is false in every build; a test-only
// build-tagged file sets it so that a test run never reaches the real hosts.
var descriptionsDisabled bool

// quayAPI is Quay's API (replaced by the tests).
var quayAPI = "https://quay.io"

// HintEnv is one variable the description names.
type HintEnv struct {
	Name string `json:"name"`
	// Example: the value the description's example uses, a hint only (never for a secret)
	Example string `json:"example,omitempty"`
	// Default: the default the description states
	Default string `json:"default,omitempty"`
	// About: the description's words for it, plain text
	About string `json:"about,omitempty"`
	// Required: the text says so near it, or every docker run example of the image sets it
	Required bool `json:"required,omitempty"`
	// Optional: the text says so near it ("optional", linuxserver's #optional)
	Optional bool `json:"optional,omitempty"`
	// Secret: the name says so (the panel's isSecretName rule)
	Secret bool `json:"secret,omitempty"`
	// AllowEmpty: a switch that allows an empty password; never to be proposed as the fix
	AllowEmpty bool `json:"allowEmpty,omitempty"`
	// From: where it was found: "run", "compose", "table", "list", "heading", "text"
	From string `json:"from"`
}

// Requirements is what the publisher's description says an image needs.
type Requirements struct {
	// Source: "description" (the repository's description was read) or "labels" (only Docs)
	Source  string    `json:"source"`
	Env     []HintEnv `json:"env"`
	Volumes []string  `json:"volumes"`
	Ports   []string  `json:"ports"`
	// Docs: documentation addresses from the image's labels, https only, never fetched by Cosmos
	Docs []string `json:"docs"`
	// Truncated: the text or what was found went past a bound
	Truncated bool `json:"truncated,omitempty"`
}

// ---- reading ----

var (
	readmes = newByteCache(2 << 20) // parsed answers by registry and repository

	readmePauseMu sync.Mutex
	readmePaused  time.Time // until when description reads wait (the API said 429)
)

// descriptionSource says where the description of ref is read, and the
// repository its examples name. "" when there is none (GHCR, other registries).
func descriptionSource(ref Reference) (address, repo string) {
	parts := strings.Split(ref.Repository, "/")
	switch {
	case ref.Registry == hubRegistry && len(parts) == 2:
		return hubAPI + "/v2/namespaces/" + url.PathEscape(parts[0]) + "/repositories/" + url.PathEscape(parts[1]), ref.Repository
	case (ref.Registry == "lscr.io" || ref.Registry == "ghcr.io") && len(parts) == 2 && parts[0] == "linuxserver":
		// linuxserver publishes the same images on Docker Hub, with the description
		return hubAPI + "/v2/namespaces/linuxserver/repositories/" + url.PathEscape(parts[1]), ref.Repository
	case ref.Registry == "quay.io" && len(parts) == 2:
		return quayAPI + "/api/v1/repository/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "?includeTags=false", ref.Repository
	}
	return "", ""
}

// descriptionHints answers what the description of ref says, from the cache,
// or read when mayRead. nil when there is nothing to say.
func descriptionHints(ctx context.Context, ref Reference, mayRead bool) *Requirements {
	if descriptionsDisabled {
		return nil
	}
	address, repo := descriptionSource(ref)
	if address == "" {
		return nil
	}
	key := ref.Registry + "|" + ref.Repository
	if item, ok := readmes.get(key, readmeKeep); ok {
		if len(item.data) > 0 {
			var req Requirements
			if json.Unmarshal(item.data, &req) == nil {
				return &req
			}
		} else if time.Since(item.at) < readmeKeepFail {
			return nil
		}
	}
	if !mayRead {
		return nil
	}
	readmePauseMu.Lock()
	paused := time.Now().Before(readmePaused)
	readmePauseMu.Unlock()
	if paused {
		return nil
	}
	var answer struct {
		FullDescription string `json:"full_description"` // Docker Hub
		Description     string `json:"description"`      // Quay
	}
	err := getJSON(ctx, address, readmeAnswerLimit, &answer)
	if err == errRateLimited {
		readmePauseMu.Lock()
		readmePaused = time.Now().Add(readmePauseFor)
		readmePauseMu.Unlock()
		return nil
	}
	if err != nil {
		if err == errNotFound || err == errUnauthorized || err == errTooLarge {
			readmes.put(key, cacheItem{size: 64})
		}
		return nil
	}
	text := answer.FullDescription
	if ref.Registry == "quay.io" {
		text = answer.Description
	}
	found := parseRequirements(text, repo)
	raw, _ := json.Marshal(found)
	readmes.put(key, cacheItem{data: raw, size: len(raw) + 64})
	return &found
}

// withDocs adds the documentation addresses of the image's labels.
func withDocs(hints *Requirements, declared *Declared) *Requirements {
	var docs []string
	if declared != nil {
		docs = docsFrom(declared.Labels)
	}
	if hints == nil {
		if len(docs) == 0 {
			return nil
		}
		return &Requirements{Source: "labels", Env: []HintEnv{}, Volumes: []string{}, Ports: []string{}, Docs: docs}
	}
	copied := *hints
	copied.Docs = docs
	if copied.Docs == nil {
		copied.Docs = []string{}
	}
	return &copied
}

// docsFrom picks the documentation addresses of the OCI labels: https only,
// no credentials, bounded.
func docsFrom(labels map[string]string) []string {
	out := []string{}
	for _, key := range []string{"org.opencontainers.image.documentation", "org.opencontainers.image.url", "org.opencontainers.image.source"} {
		text := strings.TrimSpace(labels[key])
		if text == "" || len(text) > 512 || strings.ContainsAny(text, " \"'<>\\`") {
			continue
		}
		u, err := url.Parse(text)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || strings.ContainsAny(u.Host, "%@") {
			continue
		}
		address := clean(u.String(), 512)
		seen := false
		for _, have := range out {
			seen = seen || have == address
		}
		if !seen && len(out) < maxHintDocs {
			out = append(out, address)
		}
	}
	return out
}

// ---- parsing ----

var (
	hintEnvName    = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	secretName     = regexp.MustCompile(`(?i)(PASS(WORD)?|PASSWD|SECRET|TOKEN|(^|_)KEY$|PRIVATE|CREDENTIALS?)(_|$)`)
	runCommand     = regexp.MustCompile("(?:^|[\\s$>(;&|`])(?:sudo\\s+)?(?:docker|podman)\\s+(?:container\\s+)?run\\s+(.*)$")
	headingLine    = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	bulletLine     = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	codeSpan       = regexp.MustCompile("`([^`]{1,200})`")
	htmlTag        = regexp.MustCompile(`<[^>]{0,500}>`)
	htmlComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	mdImage        = regexp.MustCompile(`!\[[^\]]{0,300}\]\([^)]{0,600}\)`)
	mdLink         = regexp.MustCompile(`\[([^\]]{0,300})\]\([^)]{0,600}\)`)
	imageLine      = regexp.MustCompile(`^(\s*)image:\s*["']?([^\s"'#]+)`)
	keyLine        = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_.-]*):(?:\s+(.*))?$`)
	defaultWords   = regexp.MustCompile("(?i)\\(?\\bdefaults?(?: value)?(?:\\s+(?:to|is))?\\s*:?\\s*`([^`]{0,128})`")
	requiredWords  = regexp.MustCompile(`(?i)\b(?:is|are)\s+(?:required|mandatory)\b|\bmust be (?:set|provided|specified|defined)\b`)
	requiredFirst  = regexp.MustCompile(`(?i)^\W*(?:required|mandatory)\b`) // "Required." as the first word of a variable's text
	conditionWords = regexp.MustCompile(`(?i)\boptional|\bnot (?:required|mandatory|needed)|\b(?:required|mandatory|needed)\s+(?:for (?:a|an|the)\b|to )|\bif\b|\bwhen\b|\bunless\b|\bno\b|\bone of\b|\beither\b|\bboth\b`)
	fileTarget     = regexp.MustCompile(`(?i)^[^.].*\.(?:conf|cnf|cfg|ini|ya?ml|json|toml|acl|sock|sh|sql|pem|crt|key|txt|xml|env|js|php|lua|db|properties)$`)
	sentenceEnd    = regexp.MustCompile(`[.!?;]\s+|\n`)
	definition     = regexp.MustCompile("^\\s*`[A-Z_][A-Z0-9_]{0,63}(?:=[^`]*)?`\\s*(?::|\\(|-|–|—)")
)

// Words that pass the grammar but are not variables (only names without an underscore are checked).
var notVariables = map[string]bool{
	"HTTP": true, "HTTPS": true, "GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true,
	"TCP": true, "UDP": true, "DNS": true, "API": true, "URL": true, "URI": true, "JSON": true, "YAML": true, "XML": true,
	"SQL": true, "TLS": true, "SSL": true, "ID": true, "OK": true, "YES": true, "NO": true, "TRUE": true, "FALSE": true,
	"NULL": true, "NIL": true, "NONE": true, "README": true, "LICENSE": true, "TODO": true, "NOTE": true, "WARNING": true,
	"IMPORTANT": true, "TIP": true, "CAUTION": true, "FAQ": true, "UTC": true, "GMT": true, "AND": true, "OR": true, "NOT": true,
	"ALL": true, "ANY": true, "ON": true, "OFF": true, "LOGIN": true, "PLAIN": true, "VOLUME": true, "EXPOSE": true,
	"ENV": true, "CMD": true, "ENTRYPOINT": true, "FROM": true, "RUN": true, "COPY": true, "ADD": true, "USER": true,
	"WORKDIR": true, "ARG": true, "LABEL": true, "HEALTHCHECK": true, "SHELL": true, "ONBUILD": true, "STOPSIGNAL": true,
}

// isSecretEnv mirrors the panel's isSecretName (client/src/utils/anyImage.js).
func isSecretEnv(name string) bool {
	return secretName.MatchString(name) && !strings.HasSuffix(strings.ToUpper(name), "_FILE")
}

// variableName answers the name if it is one this parser takes.
func variableName(text string) (string, bool) {
	name := strings.TrimSpace(text)
	if !hintEnvName.MatchString(name) || strings.Trim(name, "_") == "" {
		return "", false
	}
	if !strings.Contains(name, "_") && notVariables[name] {
		return "", false
	}
	return name, true
}

// fileVariant: X_FILE (the official images' secrets) and FILE__X (linuxserver's) are
// another way to give X, not a setting of their own (X_FILE only when X is
// known: REDIS_TLS_CERT_FILE is a setting).
func fileVariant(name string) (string, bool) {
	if strings.HasSuffix(name, "_FILE") && len(name) > len("_FILE") {
		return strings.TrimSuffix(name, "_FILE"), true
	}
	if strings.HasPrefix(name, "FILE__") && len(name) > len("FILE__") {
		return strings.TrimPrefix(name, "FILE__"), true
	}
	return "", false
}

// plainText turns a piece of markdown into bounded plain text.
func plainText(text string, n int) string {
	text = mdImage.ReplaceAllString(text, "")
	text = mdLink.ReplaceAllString(text, "$1")
	text = htmlTag.ReplaceAllString(text, "")
	text = strings.NewReplacer("`", "", "**", "", "<", "", ">", "", "\\|", "|").Replace(text)
	text = strings.Join(strings.Fields(clean(text, readmeLineLimit)), " ")
	return clean(text, n)
}

// hintValue is an example or a default: plain, bounded, without markup.
func hintValue(text string) string {
	text = strings.TrimSpace(htmlTag.ReplaceAllString(text, ""))
	text = strings.NewReplacer("<", "", ">", "", "`", "").Replace(text)
	if text == "nil" || text == "null" {
		return ""
	}
	return strings.TrimSpace(clean(text, hintExampleLen))
}

// prepareText bounds the text and removes what must never be read: control
// and direction characters, HTML comments.
func prepareText(text string) ([]string, bool) {
	truncated := false
	if len(text) > readmeTextLimit {
		text, truncated = text[:readmeTextLimit], true
	}
	text = strings.ToValidUTF8(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = htmlComment.ReplaceAllString(text, "")
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return -1
		case r == 0x200b || r == 0x200e || r == 0x200f || r == 0xfeff || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069):
			return -1 // zero width and direction marks
		}
		return r
	}, text)
	lines := strings.Split(text, "\n")
	if len(lines) > readmeMaxLines {
		lines, truncated = lines[:readmeMaxLines], true
	}
	for i, line := range lines {
		if len(line) > readmeLineLimit {
			lines[i] = clean(line, readmeLineLimit)
			truncated = true
		}
	}
	return lines, truncated
}

// imageMatches tells whether a word of an example names the image (any
// registry in front, any tag or digest after).
func imageMatches(word, repo string) bool {
	if repo == "" {
		return false
	}
	t := strings.ToLower(strings.Trim(word, `"'`))
	if at := strings.IndexByte(t, '@'); at >= 0 {
		t = t[:at]
	}
	if colon, slash := strings.LastIndexByte(t, ':'), strings.LastIndexByte(t, '/'); colon > slash {
		t = t[:colon]
	}
	repo = strings.ToLower(repo)
	if t == repo || strings.HasSuffix(t, "/"+repo) {
		return true
	}
	if name, ok := strings.CutPrefix(repo, "library/"); ok {
		return t == name || t == "docker.io/"+name || t == "index.docker.io/"+name
	}
	return false
}

// found collects what the parser finds, bounded.
type found struct {
	repo    string
	env     map[string]*HintEnv
	order   []string
	rank    map[string]int
	volumes []string
	ports   []string
	runs    []map[string]bool // the variables of each docker run example of the image
	more    bool
}

// rank of a source: what examples set comes before what tables merely list
var sourceRank = map[string]int{"run": 0, "compose": 0, "heading": 1, "list": 1, "table": 1, "text": 2}

func (f *found) add(name, from string, set func(*HintEnv)) {
	if strings.HasPrefix(name, "FILE__") {
		return // linuxserver's way to give any variable from a file
	}
	e := f.env[name]
	if e == nil {
		if len(f.order) >= 4*maxHintEnv {
			f.more = true
			return
		}
		e = &HintEnv{Name: name, From: from, Secret: isSecretEnv(name), AllowEmpty: strings.Contains(name, "ALLOW_EMPTY")}
		f.env[name] = e
		f.order = append(f.order, name)
		f.rank[name] = sourceRank[from]
	} else if sourceRank[from] < f.rank[name] {
		f.rank[name] = sourceRank[from]
	}
	if set != nil {
		set(e)
	}
}

func (f *found) describe(name, from, about, def string, required, optional bool) {
	f.add(name, from, func(e *HintEnv) {
		if e.About == "" {
			e.About = plainText(about, hintAboutLen)
		}
		if e.Default == "" {
			e.Default = hintValue(def)
		}
		e.Required = e.Required || required
		e.Optional = e.Optional || optional
	})
}

func (f *found) example(name, from, value string, optional bool) {
	f.add(name, from, func(e *HintEnv) {
		if e.Example == "" {
			e.Example = hintValue(value)
		}
		e.Optional = e.Optional || optional
	})
}

func (f *found) volume(spec string) {
	spec = strings.Trim(strings.TrimSpace(spec), `"'`)
	parts := strings.Split(spec, ":")
	target := parts[0]
	if len(parts) >= 2 {
		target = parts[1]
	}
	if !strings.HasPrefix(target, "/") || len(target) > 256 || strings.ContainsAny(target, " $`\\<>|;&*?") {
		return
	}
	target = path.Clean(target)
	if fileTarget.MatchString(path.Base(target)) {
		return // a single file (a configuration, the Docker socket): not a folder to keep
	}
	for _, have := range f.volumes {
		if have == target {
			return
		}
	}
	if len(f.volumes) >= maxHintVolumes {
		f.more = true
		return
	}
	f.volumes = append(f.volumes, clean(target, 256))
}

func (f *found) port(spec string) {
	spec = strings.Trim(strings.TrimSpace(spec), `"'`)
	proto := "tcp"
	if slash := strings.LastIndexByte(spec, '/'); slash >= 0 {
		proto = strings.ToLower(spec[slash+1:])
		spec = spec[:slash]
	}
	if proto != "tcp" && proto != "udp" && proto != "sctp" {
		return
	}
	if colon := strings.LastIndexByte(spec, ':'); colon >= 0 {
		spec = spec[colon+1:]
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < 1 || n > 65535 || !portPattern.MatchString(spec) {
		return
	}
	text := strconv.Itoa(n) + "/" + proto
	for _, have := range f.ports {
		if have == text {
			return
		}
	}
	if len(f.ports) >= maxHintPorts {
		f.more = true
		return
	}
	f.ports = append(f.ports, text)
}

// shellWords splits a command as a shell would, roughly: quotes, escapes,
// and `…` kept as one word (linuxserver's `#optional`). It stops at a
// comment, a pipe, a redirection or a list operator.
func shellWords(s string) []string {
	words := []string{}
	var b strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, b.String())
		}
		b.Reset()
		inWord = false
	}
	for i := 0; i < len(s) && len(words) < readmeMaxWords; i++ {
		c := s[i]
		switch {
		case c == ' ':
			flush()
		case c == '\'' || c == '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				end = len(s) - i - 1
			}
			b.WriteString(s[i+1 : i+1+end])
			inWord = true
			i += end + 1
		case c == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				flush()
				return words // an unclosed code span ends the command
			}
			flush()
			words = append(words, "`"+s[i+1:i+1+end])
			i += end + 1
		case c == '\\' && i+1 < len(s):
			b.WriteByte(s[i+1])
			inWord = true
			i++
		case c == '#' && !inWord:
			flush()
			return words
		case c == '|' || c == ';' || c == '&' || c == ')' || (c == '>' || c == '<') && !inWord:
			flush()
			return words // (a <placeholder> inside a word is kept)
		default:
			b.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return words
}

// flags of docker run that take a value (the others are switches)
var valueFlags = map[string]bool{}

func init() {
	for _, flag := range strings.Fields(`-e --env --env-file -v --volume -p --publish --mount --name --network --net
		-u --user -w --workdir -h --hostname --restart --entrypoint -l --label --label-file --log-driver --log-opt
		-m --memory --memory-swap --memory-reservation --cpus --cpu-shares -c --cpuset-cpus --device --cap-add --cap-drop
		--security-opt --tmpfs --ulimit --add-host --dns --dns-search --dns-option --link --shm-size --platform --pull
		--stop-signal --stop-timeout --volumes-from --ip --ip6 --mac-address --pid --ipc --uts --userns --cgroupns
		--gpus --health-cmd --health-interval --health-retries --health-timeout --health-start-period --group-add
		--sysctl --runtime --isolation --expose --network-alias --cidfile --storage-opt --blkio-weight -a --attach
		--detach-keys --cgroup-parent --domainname --oom-score-adj --pids-limit --log-level`) {
		valueFlags[flag] = true
	}
}

// runExample reads one docker run command; nothing when it runs another image.
func (f *found) runExample(command string) {
	words := shellWords(command)
	type pending struct{ name, value string }
	var envs []pending
	var optional = map[string]bool{}
	var volumes, ports []string
	names := map[string]bool{}
	last := ""
	positional := 0
	matched := false
	for i := 0; i < len(words); i++ {
		w := words[i]
		if strings.HasPrefix(w, "`") {
			if strings.Contains(strings.ToLower(w), "optional") && last != "" {
				optional[last] = true
			}
			continue
		}
		if strings.HasPrefix(w, "-") && len(w) > 1 && w != "--" {
			flag, value, hasValue := strings.Cut(w, "=")
			if !hasValue && valueFlags[flag] && i+1 < len(words) {
				i++
				value, hasValue = words[i], true
			}
			if !hasValue {
				continue
			}
			switch flag {
			case "-e", "--env":
				name, val, _ := strings.Cut(value, "=")
				if base, isFile := fileVariant(name); isFile {
					names[base] = true // X_FILE in an example sets X
				}
				if n, ok := variableName(name); ok {
					envs = append(envs, pending{n, val})
					names[n] = true
					last = n
				}
			case "-v", "--volume":
				volumes = append(volumes, value)
				last = ""
			case "--mount":
				for _, part := range strings.Split(value, ",") {
					if k, v, ok := strings.Cut(part, "="); ok && (k == "target" || k == "dst" || k == "destination") {
						volumes = append(volumes, v)
					}
				}
				last = ""
			case "-p", "--publish":
				ports = append(ports, value)
				last = ""
			default:
				last = ""
			}
			continue
		}
		// the image: the first word that is not a flag (or the second, after an unknown flag's value)
		positional++
		if imageMatches(w, f.repo) {
			matched = true
			break
		}
		if positional >= 2 {
			break
		}
	}
	if !matched {
		return
	}
	for _, e := range envs {
		f.example(e.name, "run", e.value, optional[e.name])
	}
	for _, v := range volumes {
		f.volume(v)
	}
	for _, p := range ports {
		f.port(p)
	}
	if len(f.runs) < readmeMaxCommands {
		f.runs = append(f.runs, names)
	}
}

// commands finds the docker run commands of the text, continued lines joined.
func commands(lines []string) []string {
	out := []string{}
	for i := 0; i < len(lines) && len(out) < readmeMaxCommands; i++ {
		m := runCommand.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		command := m[1]
		for j := 0; j < 64 && len(command) < readmeJoinLimit; j++ {
			trimmed := strings.TrimRight(command, " ")
			if !strings.HasSuffix(trimmed, "\\") || i+1 >= len(lines) {
				break
			}
			trimmed = strings.TrimSuffix(trimmed, "\\")
			i++
			command = trimmed + " " + strings.TrimLeft(lines[i], " $>")
		}
		out = append(out, command)
	}
	return out
}

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

// unquote takes a YAML scalar's quotes and trailing comment away.
func yamlScalar(text string) (string, string) {
	text = strings.TrimSpace(text)
	comment := ""
	if strings.HasPrefix(text, `"`) || strings.HasPrefix(text, `'`) {
		q := text[0]
		if end := strings.IndexByte(text[1:], q); end >= 0 {
			rest := text[end+2:]
			if hash := strings.Index(rest, "#"); hash >= 0 {
				comment = rest[hash:]
			}
			return text[1 : end+1], comment
		}
	}
	if hash := strings.Index(text, " #"); hash >= 0 {
		comment = text[hash:]
		text = text[:hash]
	} else if strings.HasPrefix(text, "#") {
		return "", text
	}
	return strings.TrimSpace(text), comment
}

// compose reads the services of a compose file in a code block that run the image.
func (f *found) compose(block []string) {
	for i, line := range block {
		m := imageLine.FindStringSubmatch(line)
		if m == nil || !imageMatches(m[2], f.repo) {
			continue
		}
		keyIndent := len(m[1])
		// the service's block: up to the image line, back to its header; after it, while deeper
		start := i
		for start > 0 {
			prev := block[start-1]
			if strings.TrimSpace(prev) != "" && !strings.HasPrefix(strings.TrimSpace(prev), "#") && indentOf(prev) < keyIndent {
				break
			}
			start--
		}
		end := i + 1
		for end < len(block) {
			next := block[end]
			if strings.TrimSpace(next) != "" && !strings.HasPrefix(strings.TrimSpace(next), "#") && indentOf(next) < keyIndent {
				break
			}
			end++
		}
		for k := start; k < end; k++ {
			key := keyLine.FindStringSubmatch(block[k])
			if key == nil || len(key[1]) != keyIndent {
				continue
			}
			section := key[2]
			if section != "environment" && section != "volumes" && section != "ports" {
				continue
			}
			for c := k + 1; c < end; c++ {
				child := block[c]
				trimmed := strings.TrimSpace(child)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				if indentOf(child) <= keyIndent {
					break
				}
				item, isList := strings.CutPrefix(trimmed, "- ")
				value, comment := yamlScalar(item)
				optional := strings.Contains(strings.ToLower(comment), "optional")
				switch section {
				case "environment":
					if isList {
						name, val, _ := strings.Cut(value, "=")
						if n, ok := variableName(name); ok {
							f.example(n, "compose", val, optional)
						}
					} else if kv := keyLine.FindStringSubmatch(child); kv != nil {
						val, comment := yamlScalar(kv[3])
						if n, ok := variableName(kv[2]); ok {
							f.example(n, "compose", val, strings.Contains(strings.ToLower(comment), "optional"))
						}
					}
				case "volumes":
					if isList && !strings.Contains(value, ": ") {
						f.volume(value)
					}
				case "ports":
					if isList && !strings.Contains(value, ": ") {
						f.port(value)
					}
				}
			}
		}
	}
}

// heading is a markdown heading with what it means for the text under it.
type heading struct {
	level int
	env   bool // about environment variables
}

func headingIsEnv(text string) (env, readOnly bool) {
	t := strings.ToLower(text)
	readOnly = strings.Contains(t, "read-only") || strings.Contains(t, "readonly") || strings.Contains(t, "read only")
	env = strings.Contains(t, "environment variable") || strings.Contains(t, "env var") || strings.Contains(t, "environment") && strings.Contains(t, "variable") ||
		t == "environment" || t == "variables" || t == "configuration variables"
	return env, readOnly
}

// headingNames: a heading that is only variable names in code ("`A`, `B`").
func headingNames(text string) []string {
	spans := codeSpan.FindAllStringSubmatch(text, 8)
	if len(spans) == 0 {
		return nil
	}
	rest := strings.ToLower(codeSpan.ReplaceAllString(text, ""))
	rest = strings.NewReplacer(",", "", "/", "", "&", "", " and ", " ", " or ", " ").Replace(rest)
	if strings.TrimSpace(rest) != "" {
		return nil
	}
	names := []string{}
	for _, s := range spans {
		if n, ok := variableName(s[1]); ok {
			names = append(names, n)
		}
	}
	return names
}

// explicitRequired: the variable's own text says it is required, without a condition.
func explicitRequired(text string) bool {
	if saysOptional(text) {
		return false
	}
	for i, sentence := range sentenceEnd.Split(text, 32) {
		if (requiredWords.MatchString(sentence) || i == 0 && requiredFirst.MatchString(sentence)) && !conditionWords.MatchString(sentence) {
			return true
		}
	}
	return false
}

func saysOptional(text string) bool {
	return strings.Contains(strings.ToLower(text), "optional")
}

// splitRow splits a markdown table row on the pipes that are not escaped.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := []string{}
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && i+1 < len(line) && line[i+1] == '|' {
			b.WriteByte('|')
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteByte(line[i])
	}
	return append(cells, strings.TrimSpace(b.String()))
}

func isSeparatorRow(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return len(cells) > 0
}

// table reads a markdown table: flag tables (linuxserver's `-e PUID=1000`) always,
// tables of names under a heading about variables or with a variable column.
func (f *found) table(rows []string, env bool) {
	if len(rows) < 3 {
		return
	}
	header := splitRow(rows[0])
	if !isSeparatorRow(splitRow(rows[1])) {
		return
	}
	nameCol, defCol, descCol, reqCol := 0, -1, -1, -1
	variableColumn := false
	for i, h := range header {
		h = strings.ToLower(plainText(h, 64))
		switch {
		case strings.Contains(h, "required") || strings.Contains(h, "mandatory"):
			reqCol = i
		case strings.Contains(h, "default"):
			defCol = i
		case strings.Contains(h, "description") || strings.Contains(h, "function") || strings.Contains(h, "purpose") || strings.Contains(h, "meaning") || strings.Contains(h, "details"):
			descCol = i
		case strings.Contains(h, "variable") || strings.Contains(h, "env"):
			nameCol, variableColumn = i, true
		case h == "name" || h == "key" || h == "parameter" || h == "option" || h == "setting":
			nameCol = i
		}
	}
	if descCol < 0 && len(header) > 1 {
		descCol = len(header) - 1
		if descCol == nameCol {
			descCol = -1
		}
	}
	cell := func(cells []string, i int) string {
		if i >= 0 && i < len(cells) {
			return cells[i]
		}
		return ""
	}
	for _, row := range rows[2:] {
		cells := splitRow(row)
		first := strings.TrimSpace(cell(cells, nameCol))
		about := cell(cells, descCol)
		optional := saysOptional(about)
		required := explicitRequired(about)
		if reqCol >= 0 {
			switch strings.ToLower(plainText(cell(cells, reqCol), 16)) {
			case "yes", "true", "required", "mandatory", "x", "✓", "✔", "✅":
				required = true
			case "no", "false", "optional", "":
				optional = optional || strings.TrimSpace(cell(cells, reqCol)) != ""
			}
		}
		spans := codeSpan.FindStringSubmatch(first)
		inner := strings.TrimSpace(strings.Trim(first, "*"))
		if spans != nil {
			inner = strings.TrimSpace(spans[1])
		}
		if strings.HasPrefix(inner, "-") {
			// a flag table: `-e NAME=value`, `-p 80:80`, `-v /config`
			flag, value, _ := strings.Cut(inner, " ")
			value = strings.TrimSpace(value)
			switch flag {
			case "-e", "--env":
				name, val, _ := strings.Cut(value, "=")
				if n, ok := variableName(name); ok {
					f.example(n, "table", val, optional)
					f.describe(n, "table", about, "", required && !optional, optional)
				}
			case "-p", "--publish":
				f.port(value)
			case "-v", "--volume":
				f.volume(value)
			}
			continue
		}
		if !env && !variableColumn {
			continue
		}
		name, def, hasDef := strings.Cut(inner, "=")
		n, ok := variableName(name)
		if !ok {
			continue
		}
		defText := ""
		if defCol >= 0 {
			defText = plainText(cell(cells, defCol), hintExampleLen)
		} else if hasDef {
			defText = def
		}
		f.describe(n, "table", about, defText, required && !optional, optional)
	}
}

// bullet reads a list item that starts with a variable's name in code.
func (f *found) bullet(item string) bool {
	trimmed := strings.TrimSpace(item)
	var first string
	switch {
	case strings.HasPrefix(trimmed, "`"):
		end := strings.IndexByte(trimmed[1:], '`')
		if end < 0 {
			return false
		}
		first, trimmed = trimmed[1:end+1], trimmed[end+2:]
	case strings.HasPrefix(trimmed, "**"):
		end := strings.Index(trimmed[2:], "**")
		if end < 0 {
			return false
		}
		first, trimmed = strings.Trim(trimmed[2:end+2], "`"), trimmed[end+4:]
	default:
		return false
	}
	name, example, _ := strings.Cut(first, "=")
	n, ok := variableName(strings.TrimPrefix(strings.TrimPrefix(name, "-e "), "--env "))
	if !ok {
		return false
	}
	about := strings.TrimLeft(trimmed, " :-–—")
	def := ""
	if m := defaultWords.FindStringSubmatch(about); m != nil {
		def = m[1]
	}
	optional := saysOptional(about)
	f.describe(n, "list", about, def, explicitRequired(about) && !optional, optional)
	if example != "" {
		f.example(n, "list", example, false)
	}
	return true
}

// inline reads the variables of code spans in a line of prose: `-e NAME=value`
// anywhere, `NAME=value` where the line or the section is about variables.
func (f *found) inline(line string, env bool) {
	about := strings.Contains(strings.ToLower(line), "variable")
	for _, m := range codeSpan.FindAllStringSubmatch(line, 16) {
		span := strings.TrimSpace(m[1])
		if strings.HasPrefix(span, "-e ") || strings.HasPrefix(span, "--env ") || strings.HasPrefix(span, "--env=") {
			words := shellWords(span)
			for i := 0; i < len(words); i++ {
				value := ""
				switch {
				case (words[i] == "-e" || words[i] == "--env") && i+1 < len(words):
					value = words[i+1]
					i++
				case strings.HasPrefix(words[i], "--env="):
					value = strings.TrimPrefix(words[i], "--env=")
				default:
					continue
				}
				name, val, _ := strings.Cut(value, "=")
				if n, ok := variableName(name); ok {
					f.example(n, "text", val, saysOptional(line))
				}
			}
			continue
		}
		if (env || about) && strings.Contains(span, "=") && !strings.ContainsAny(span, " ") {
			name, val, _ := strings.Cut(span, "=")
			if n, ok := variableName(name); ok {
				f.example(n, "text", val, false)
			}
		}
	}
}

// parseRequirements finds what a description says an image needs: the
// variables, volumes and ports of its examples (docker run, compose) for the
// image repo, and the variables its tables, lists and headings about
// variables document. Precision before recall: a variable is required only
// when its own text says so without a condition, or when every docker run
// example of the image (two at least) sets it.
func parseRequirements(text, repo string) Requirements {
	lines, truncated := prepareText(text)
	f := &found{repo: repo, env: map[string]*HintEnv{}, rank: map[string]int{}, more: truncated}

	// examples: docker run anywhere, compose in code blocks
	for _, command := range commands(lines) {
		f.runExample(command)
	}
	inFence := false
	var block []string
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			if inFence {
				f.compose(block)
				block = nil
			}
			inFence = !inFence
			continue
		}
		if inFence && len(block) < 2000 {
			block = append(block, line)
		}
	}
	if inFence {
		f.compose(block)
	}

	// documentation: headings, tables, lists, prose
	var stack []heading
	inEnv := func() bool { return len(stack) > 0 && stack[len(stack)-1].env }
	inFence = false
	var headingVars []string // the names of the last variable heading, waiting for its first paragraph
	var paragraph []string
	var table []string
	lastProse := ""
	inList := false
	flushParagraph := func() {
		if len(headingVars) > 0 && len(paragraph) > 0 {
			about := strings.Join(paragraph, " ")
			for _, n := range headingVars {
				optional := saysOptional(about)
				f.describe(n, "heading", about, "", explicitRequired(about) && !optional, optional)
			}
			headingVars = nil
		}
		paragraph = nil
	}
	flushTable := func() {
		if len(table) > 0 {
			f.table(table, inEnv() || strings.Contains(strings.ToLower(lastProse), "variable"))
		}
		table = nil
	}
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			flushTable()
			flushParagraph()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(t, "|") {
			table = append(table, t)
			continue
		}
		flushTable()
		if m := headingLine.FindStringSubmatch(line); m != nil {
			flushParagraph()
			for _, n := range headingVars {
				f.describe(n, "heading", "", "", false, false)
			}
			headingVars = nil
			level := len(m[1])
			title := strings.TrimSpace(htmlTag.ReplaceAllString(m[2], ""))
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}
			parentEnv := inEnv()
			env, readOnly := headingIsEnv(plainText(title, 200))
			names := headingNames(title)
			h := heading{level: level, env: (env || parentEnv) && !readOnly}
			if parentEnv && len(names) > 0 {
				headingVars = names
			}
			stack = append(stack, h)
			lastProse, inList = "", false
			continue
		}
		if t == "" {
			if len(paragraph) > 0 {
				flushParagraph()
			}
			continue
		}
		if m := bulletLine.FindStringSubmatch(line); m != nil {
			// a list about variables, or an item shaped as one: "`NAME`: …", "`NAME` (default …)"
			introduced := inEnv() || strings.Contains(strings.ToLower(lastProse), "variable") || definition.MatchString(m[1])
			if introduced && f.bullet(m[1]) {
				inList = true
				continue
			}
		}
		if inList && indentOf(line) > 0 {
			continue // the rest of a list item
		}
		inList = false
		if len(headingVars) > 0 {
			paragraph = append(paragraph, t)
		}
		f.inline(t, inEnv())
		lastProse = t
	}
	flushTable()
	flushParagraph()
	for _, n := range headingVars {
		f.describe(n, "heading", "", "", false, false)
	}

	// required by the examples: set by every docker run example of the image
	if len(f.runs) >= 2 {
		for name, e := range f.env {
			every := true
			for _, run := range f.runs {
				every = every && run[name]
			}
			if every && !e.Optional {
				e.Required = true
			}
		}
	}
	out := Requirements{Source: "description", Env: []HintEnv{}, Volumes: f.volumes, Ports: f.ports, Docs: []string{}, Truncated: f.more}
	if out.Volumes == nil {
		out.Volumes = []string{}
	}
	if out.Ports == nil {
		out.Ports = []string{}
	}
	// required first, then what examples set, then the rest, each in the text's order
	position := map[string]int{}
	for i, n := range f.order {
		position[n] = i
	}
	names := append([]string{}, f.order...)
	sort.SliceStable(names, func(i, j int) bool {
		a, b := f.env[names[i]], f.env[names[j]]
		if a.AllowEmpty {
			a.Required = false
		}
		if b.AllowEmpty {
			b.Required = false
		}
		if a.Required != b.Required {
			return a.Required
		}
		if f.rank[a.Name] != f.rank[b.Name] {
			return f.rank[a.Name] < f.rank[b.Name]
		}
		return position[a.Name] < position[b.Name]
	})
	for _, n := range names {
		if base, isFile := fileVariant(n); isFile && f.env[base] != nil {
			continue
		}
		e := *f.env[n]
		if e.AllowEmpty {
			e.Required = false
		}
		if e.Required && e.Optional {
			e.Required = false // the text disagrees with itself: not required
		}
		if e.Secret {
			e.Example = "" // a documented password is never offered
		}
		e.Name = clean(e.Name, 64)
		if len(out.Env) >= maxHintEnv {
			out.Truncated = true
			break
		}
		out.Env = append(out.Env, e)
	}
	return out
}
