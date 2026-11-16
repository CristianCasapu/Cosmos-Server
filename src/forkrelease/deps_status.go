package forkrelease

// Fork addition: the status of the automatic dependency updates, which the
// workflow fork-deps-update.yml writes to the branch fork-deps-status of the
// repository the server updates from, and the dependency summary a release
// carries in its signed meta.json.
//
// The status is not signed: it is public text that anyone who can push to
// that branch controls, so it is read as hostile input. It is fetched under
// the updater's rules (HTTPS, GitHub's hosts only, redirects only to them),
// bounded in size and time, and parsed strictly: a value of the wrong type
// or shape makes the whole status invalid, unknown fields are ignored,
// texts are cleaned of control characters and cut, and links are never
// taken from it: they are built from the run and issue numbers.

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxDepsStatus bounds status.json.
	MaxDepsStatus = 64 << 10
	// DepsStatusTimeout bounds its request.
	DepsStatusTimeout = 20 * time.Second

	depsStatusBranch = "fork-deps-status"

	maxDepsChanges = 300
	maxDepsList    = 50
	maxFailure     = 1500
	maxReason      = 300
	maxDepsRun     = 1 << 50
	maxDepsIssue   = 1 << 31

	// MaxMetaFile bounds a meta.json read for its dependency summary.
	MaxMetaFile        = 64 << 10
	maxMetaCount       = 10000
	maxMetaMajors      = 10
	maxMetaMajorLength = 80
)

// The values a status may carry.
var (
	DepsOutcomes    = []string{"updated", "nothing", "failed", "paused"}
	DepsEcosystems  = []string{"go", "npm", "action", "node", "image", "toolchain"}
	DepsKinds       = []string{"patch", "minor", "major"}
	DepsFailedSteps = []string{"update", "advisories", "checks", "push", "custom-moved", "release"}
)

var (
	ErrDepsStatus = errors.New("the dependency status is not valid")

	depsNamePattern   = regexp.MustCompile(`^[A-Za-z0-9@/._+-]{1,214}$`)
	depsVerPattern    = regexp.MustCompile(`^[A-Za-z0-9.+_-]{1,64}$`)
	metaMajorPattern  = regexp.MustCompile(`^[A-Za-z0-9@/._+ -]{1,80}$`)
	quietSincePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// DepsChange is one dependency in a list of the status.
type DepsChange struct {
	Eco     string
	Name    string
	From    string
	To      string
	Kind    string // changes only: patch, minor or major
	AgeDays int    // changes only: how old the new version was
	Reason  string // the other lists: why, cleaned text
}

// DepsStatus is a status.json that passed the checks.
type DepsStatus struct {
	Run        int64
	At         time.Time
	Outcome    string
	Release    string    // x.y.z-fork.N, or "" (always set when updated)
	QuietSince time.Time // zero when not given
	FailedStep string
	Issue      int64
	Failure    string // cleaned, line breaks kept
	Changes    []DepsChange
	Dropped    []DepsChange
	Tried      []DepsChange
	Left       []DepsChange

	// built from the numbers, never taken from the status
	RunLink   string
	IssueLink string
}

// ValidRepo reports whether a repository name is owner/name as GitHub
// allows it (repoPattern is install.go's).
func ValidRepo(repo string) bool {
	return repoPattern.MatchString(repo) && !strings.Contains(repo, "..")
}

// DepsStatusURL is the address of the status of a repository.
func DepsStatusURL(repo string) (string, error) {
	if !ValidRepo(repo) {
		return "", ErrAddress
	}
	return "https://raw.githubusercontent.com/" + repo + "/refs/heads/" + depsStatusBranch + "/status.json", nil
}

// FetchDepsStatus reads the status of a repository, bounded. It does not
// parse it: see ParseDepsStatus.
func FetchDepsStatus(repo string) ([]byte, error) {
	return fetcher{}.fetchDepsStatus(repo)
}

func (f fetcher) fetchDepsStatus(repo string) ([]byte, error) {
	address, err := DepsStatusURL(repo)
	if err != nil {
		return nil, err
	}
	return f.get(address, MaxDepsStatus, DepsStatusTimeout)
}

// rawStatus is what is decoded: a field of another JSON type fails the
// decoding, a missing one stays nil.
type rawStatus struct {
	Schema     *int        `json:"schema"`
	Run        *int64      `json:"run"`
	At         *string     `json:"at"`
	Outcome    *string     `json:"outcome"`
	Release    *string     `json:"release"`
	QuietSince *string     `json:"quietSince"`
	FailedStep *string     `json:"failedStep"`
	Issue      *int64      `json:"issue"`
	Failure    *string     `json:"failure"`
	Changes    []rawChange `json:"changes"`
	Dropped    []rawChange `json:"dropped"`
	Tried      []rawChange `json:"tried"`
	Left       []rawChange `json:"left"`
}

type rawChange struct {
	Eco     *string `json:"eco"`
	Name    *string `json:"name"`
	From    *string `json:"from"`
	To      *string `json:"to"`
	Kind    *string `json:"kind"`
	AgeDays *int    `json:"ageDays"`
	Reason  *string `json:"reason"`
}

func oneOf(value string, list []string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func invalid(why string) error {
	return errors.New(ErrDepsStatus.Error() + ": " + why)
}

// CleanStatusText removes control and direction characters (line breaks
// and tabs too, unless keepLines), keeps valid UTF-8 and cuts to max runes.
func CleanStatusText(text string, max int, keepLines bool) string {
	text = strings.ToValidUTF8(text, "?")
	text = strings.Map(func(r rune) rune {
		if keepLines && (r == '\n' || r == '\t') {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 ||
			(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f ||
			r == 0x061c || r == 0xfeff {
			if r == '\r' && keepLines {
				return -1
			}
			return ' '
		}
		return r
	}, text)
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > max {
		text = string([]rune(text)[:max-1]) + "…"
	}
	return text
}

func parseList(raw []rawChange, max int, changes bool) ([]DepsChange, error) {
	if len(raw) > max {
		return nil, invalid("too many entries")
	}
	out := make([]DepsChange, 0, len(raw))
	for _, r := range raw {
		if r.Eco == nil || r.Name == nil || r.From == nil || r.To == nil {
			return nil, invalid("an entry misses a field")
		}
		c := DepsChange{Eco: *r.Eco, Name: *r.Name, From: *r.From, To: *r.To}
		if !oneOf(c.Eco, DepsEcosystems) {
			return nil, invalid("unknown ecosystem")
		}
		if !depsNamePattern.MatchString(c.Name) {
			return nil, invalid("a name is not one")
		}
		if !depsVerPattern.MatchString(c.From) || !depsVerPattern.MatchString(c.To) {
			return nil, invalid("a version is not one")
		}
		if changes {
			if r.Kind == nil || !oneOf(*r.Kind, DepsKinds) {
				return nil, invalid("unknown kind of change")
			}
			c.Kind = *r.Kind
			if r.AgeDays != nil {
				if *r.AgeDays < 0 || *r.AgeDays > 100000 {
					return nil, invalid("an age is out of range")
				}
				c.AgeDays = *r.AgeDays
			}
		} else if r.Reason != nil {
			c.Reason = CleanStatusText(*r.Reason, maxReason, false)
		}
		out = append(out, c)
	}
	return out, nil
}

// ParseDepsStatus checks a status.json of the repository repo. Anything
// that does not pass is an error; the caller ignores the status then.
func ParseDepsStatus(body []byte, repo string) (DepsStatus, error) {
	var st DepsStatus
	if len(body) > MaxDepsStatus {
		return st, invalid("too large")
	}
	if !ValidRepo(repo) {
		return st, invalid("no repository")
	}
	if !utf8.Valid(body) {
		return st, invalid("not UTF-8")
	}
	var raw rawStatus
	if err := json.Unmarshal(body, &raw); err != nil {
		return st, invalid("not the expected JSON")
	}
	if raw.Schema == nil || *raw.Schema != 1 {
		return st, invalid("unknown schema")
	}
	if raw.Run == nil || *raw.Run <= 0 || *raw.Run > maxDepsRun {
		return st, invalid("no run number")
	}
	st.Run = *raw.Run
	if raw.At == nil || len(*raw.At) > 40 {
		return st, invalid("no time")
	}
	at, err := time.Parse(time.RFC3339, *raw.At)
	if err != nil || at.Year() < 2020 || at.Year() > 9999 {
		return st, invalid("no time")
	}
	st.At = at.UTC()
	if raw.Outcome == nil || !oneOf(*raw.Outcome, DepsOutcomes) {
		return st, invalid("unknown outcome")
	}
	st.Outcome = *raw.Outcome
	if raw.Release != nil && *raw.Release != "" {
		if !versionPattern.MatchString(*raw.Release) {
			return st, invalid("the release is not a fork release")
		}
		st.Release = *raw.Release
	}
	if st.Outcome == "updated" && st.Release == "" {
		return st, invalid("an update without its release")
	}
	if raw.QuietSince != nil && *raw.QuietSince != "" {
		if !quietSincePattern.MatchString(*raw.QuietSince) {
			return st, invalid("quietSince is not a date")
		}
		day, err := time.Parse("2006-01-02", *raw.QuietSince)
		if err != nil || day.Year() < 2020 {
			return st, invalid("quietSince is not a date")
		}
		st.QuietSince = day
	}
	if raw.FailedStep != nil && *raw.FailedStep != "" {
		if !oneOf(*raw.FailedStep, DepsFailedSteps) {
			return st, invalid("unknown step")
		}
		st.FailedStep = *raw.FailedStep
	}
	if raw.Issue != nil {
		if *raw.Issue <= 0 || *raw.Issue > maxDepsIssue {
			return st, invalid("not an issue number")
		}
		st.Issue = *raw.Issue
	}
	if raw.Failure != nil {
		st.Failure = CleanStatusText(*raw.Failure, maxFailure, true)
	}
	if st.Changes, err = parseList(raw.Changes, maxDepsChanges, true); err != nil {
		return DepsStatus{}, err
	}
	if st.Dropped, err = parseList(raw.Dropped, maxDepsList, false); err != nil {
		return DepsStatus{}, err
	}
	if st.Tried, err = parseList(raw.Tried, maxDepsList, false); err != nil {
		return DepsStatus{}, err
	}
	if st.Left, err = parseList(raw.Left, maxDepsList, false); err != nil {
		return DepsStatus{}, err
	}

	st.RunLink = "https://github.com/" + repo + "/actions/runs/" + strconv.FormatInt(st.Run, 10)
	if st.Issue > 0 {
		st.IssueLink = "https://github.com/" + repo + "/issues/" + strconv.FormatInt(st.Issue, 10)
	}
	return st, nil
}

// ForkDependencies is the dependency summary of a release, from its signed
// meta.json.
type ForkDependencies struct {
	Minor  int
	Major  int
	Majors []string // "react 19.0.0"
}

// ParseForkDependencies reads "forkDependencies" of a meta.json. A meta.json
// without it gives nil and no error; one with a value out of bounds gives an
// error. A summary of no change at all is nil.
func ParseForkDependencies(meta []byte) (*ForkDependencies, error) {
	if len(meta) > MaxMetaFile {
		return nil, errors.New("meta.json is too large")
	}
	var raw struct {
		Deps *struct {
			Minor  *int     `json:"minor"`
			Major  *int     `json:"major"`
			Majors []string `json:"majors"`
		} `json:"forkDependencies"`
	}
	if err := json.Unmarshal(meta, &raw); err != nil {
		return nil, errors.New("meta.json's dependency summary is not the expected JSON")
	}
	if raw.Deps == nil {
		return nil, nil
	}
	d := &ForkDependencies{}
	if raw.Deps.Minor != nil {
		d.Minor = *raw.Deps.Minor
	}
	if raw.Deps.Major != nil {
		d.Major = *raw.Deps.Major
	}
	if d.Minor < 0 || d.Minor > maxMetaCount || d.Major < 0 || d.Major > maxMetaCount {
		return nil, errors.New("meta.json's dependency counts are out of range")
	}
	if len(raw.Deps.Majors) > maxMetaMajors {
		return nil, errors.New("meta.json names too many majors")
	}
	for _, m := range raw.Deps.Majors {
		if len(m) > maxMetaMajorLength || !metaMajorPattern.MatchString(m) || strings.TrimSpace(m) != m {
			return nil, errors.New("meta.json names a major that is not one")
		}
		d.Majors = append(d.Majors, m)
	}
	if d.Minor == 0 && d.Major == 0 {
		return nil, nil
	}
	return d, nil
}

// maxSummaryMajors is how many majors a summary names.
const maxSummaryMajors = 3

// DependencySummary says what changed: "14 minor, 1 major (react 19.0.0)".
func DependencySummary(minor int, major int, majors []string) string {
	parts := []string{}
	if minor > 0 {
		parts = append(parts, strconv.Itoa(minor)+" minor")
	}
	if major > 0 {
		text := strconv.Itoa(major) + " major"
		if len(majors) > 0 {
			named := majors
			if len(named) > maxSummaryMajors {
				named = named[:maxSummaryMajors]
			}
			text += " (" + strings.Join(named, ", ")
			if more := major - len(named); more > 0 {
				text += " and " + strconv.Itoa(more) + " more"
			}
			text += ")"
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "no version change"
	}
	return strings.Join(parts, ", ")
}

// Summary of a status's changes: patches count with the minors.
func (st DepsStatus) Summary() (minor int, major int, majors []string) {
	for _, c := range st.Changes {
		if c.Kind == "major" {
			major++
			majors = append(majors, c.Name+" "+c.To)
		} else {
			minor++
		}
	}
	return minor, major, majors
}
