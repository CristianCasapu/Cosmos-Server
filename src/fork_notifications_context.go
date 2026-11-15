package main

// Fork addition: what the details of a notification show around it, read
// when they are opened: the state of the thing now, the entries of the
// security record and the Monitoring events of the same source around that
// time, and the server's log lines of that moment. For administrators only
// (the route checks); each part is bounded in time and size and says when
// it could not be read.

import (
	"bufio"
	"context"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/azukaar/cosmos-server/src/certs"
	"github.com/azukaar/cosmos-server/src/docker"
	"github.com/azukaar/cosmos-server/src/fail2ban"
	"github.com/azukaar/cosmos-server/src/security"
	"github.com/azukaar/cosmos-server/src/snapshots"
	"github.com/azukaar/cosmos-server/src/telegram"
	"github.com/azukaar/cosmos-server/src/upnp"
	"github.com/azukaar/cosmos-server/src/utils"
)

const (
	contextSecurityMax = 20
	contextEventsMax   = 20
	contextLogLines    = 60
	contextLogLineLen  = 500
	contextLogTail     = 8 << 20
	contextLogWindow   = 3 * time.Minute
	contextTimeout     = 3 * time.Second
)

type contextLine = utils.NotificationLine

type contextState struct {
	OK       *bool                        `json:"ok"`
	Sentence *contextLine                 `json:"sentence"`
	Facts    []utils.NotificationFactView `json:"facts"`
	Header   []contextLine                `json:"header"`
	Rows     []contextRow                 `json:"rows"`
	Link     string                       `json:"link,omitempty"`
}

type contextRow struct {
	Cells []string `json:"cells"`
}

type contextSecurity struct {
	Available bool             `json:"available"`
	Events    []security.Event `json:"events"`
}

type contextEvents struct {
	Available bool          `json:"available"`
	Events    []utils.Event `json:"events"`
}

type contextLog struct {
	Available bool         `json:"available"`
	Reason    *contextLine `json:"reason"`
	Lines     []string     `json:"lines"`
	Truncated bool         `json:"truncated"`
}

type notificationContextAnswer struct {
	State    *contextState   `json:"state"`
	Security contextSecurity `json:"security"`
	Events   contextEvents   `json:"events"`
	Log      contextLog      `json:"log"`
}

func line(key string, pairs ...string) *contextLine {
	t := utils.NText(key, pairs...)
	return &contextLine{Key: t.Key, Text: utils.NotificationEnglish(t.Key), Vars: t.Vars}
}

func fact(key string, value string) utils.NotificationFactView {
	return utils.NotificationFactView{Key: key, Text: utils.NotificationEnglish(key), Value: utils.MaskNotificationText(value)}
}

func flag(v bool) *bool { return &v }

// window is the time the notification covers, widened by margin.
func window(d utils.NotificationDetails, margin time.Duration) (time.Time, time.Time) {
	from := d.Date
	if d.FirstSeen != nil {
		from = *d.FirstSeen
	}
	for _, seen := range d.Seen {
		if seen.Before(from) {
			from = seen
		}
	}
	to := d.Date
	if d.Resolved != nil && d.Resolved.After(to) {
		to = *d.Resolved
	}
	return from.Add(-margin), to.Add(margin)
}

// runBounded runs a reading that may wait on the network or Docker, and
// gives up after contextTimeout.
func runBounded(read func()) bool {
	done := make(chan struct{})
	go func() {
		defer func() { recover(); close(done) }()
		read()
	}()
	select {
	case <-done:
		return true
	case <-time.After(contextTimeout):
		return false
	}
}

func notificationContext(d utils.NotificationDetails) notificationContextAnswer {
	answer := notificationContextAnswer{
		Security: contextSecurity{Events: []security.Event{}},
		Events:   contextEvents{Events: []utils.Event{}},
		Log:      contextLog{Lines: []string{}},
	}
	var state *contextState
	if runBounded(func() { state = stateNow(d) }) {
		answer.State = state
	}
	answer.Security = relatedSecurity(d)
	answer.Events = relatedEvents(d)
	answer.Log = logAround(d, utils.CONFIGFOLDER+"cosmos.plain.log")
	return answer
}

// --- the state now

func stateNow(d utils.NotificationDetails) *contextState {
	subject := ""
	if d.Subject != nil {
		subject = d.Subject.Name
	}
	switch d.Source {
	case "upnp":
		return upnpState(d)
	case "certificates":
		return certificateState(subject)
	case "containers":
		if d.Kind == "container.leftover" {
			return leftoverState(d)
		}
		return containerState(subject)
	case "snapshots":
		return snapshotsState()
	case "monitoring":
		if d.Kind == "alert.disk" {
			return diskState(subject)
		}
	case "fail2ban":
		return fail2banState()
	case "telegram":
		return telegramState()
	}
	return nil
}

func upnpState(d utils.NotificationDetails) *contextState {
	st := upnp.GetStatus()
	state := &contextState{Link: "/cosmos-ui/config-general/http-server", Facts: []utils.NotificationFactView{},
		Header: []contextLine{*line("notif.state.col.port"), *line("notif.state.col.to"), *line("notif.state.col.what")}, Rows: []contextRow{}}
	router := st.GatewayIP
	if st.Gateway != "" && router != "" {
		router = st.Gateway + " (" + st.GatewayIP + ")"
	}
	if router != "" {
		state.Facts = append(state.Facts, fact("notif.fact.router", router))
	}
	if st.ExternalIP != "" {
		state.Facts = append(state.Facts, fact("notif.fact.external", st.ExternalIP))
	}
	if !st.LastRefresh.IsZero() {
		state.Facts = append(state.Facts, fact("notif.fact.lastAsked", st.LastRefresh.Local().Format("2006-01-02 15:04:05")))
	}
	for _, m := range append(append([]upnp.Mapping{}, st.Mappings...), st.Apps...) {
		if m.Active {
			name := "Cosmos " + m.Name
			if m.Kind == "app" {
				name = m.Container
			}
			state.Rows = append(state.Rows, contextRow{Cells: []string{strconv.Itoa(m.ExternalPort) + "/" + m.Protocol, st.LocalIP + ":" + strconv.Itoa(m.InternalPort), name}})
		}
	}
	for _, f := range st.Foreign {
		state.Rows = append(state.Rows, contextRow{Cells: []string{strconv.Itoa(f.ExternalPort) + "/" + f.Protocol,
			f.InternalClient + ":" + strconv.Itoa(f.InternalPort), utils.MaskNotificationText(f.Description)}})
	}

	if !st.Enabled && len(st.Apps) == 0 {
		state.Sentence = line("notif.state.upnp.off")
		return state
	}
	if st.Paused {
		state.OK = flag(false)
		at := ""
		if st.RetryAt != nil {
			at = st.RetryAt.Local().Format("15:04")
		}
		state.Sentence = line("notif.state.upnp.paused", "at", at)
		return state
	}
	key := strings.TrimPrefix(d.NotificationView.Thread, "upnp:")
	if key == "router" || d.Kind == "upnp.router.unreachable" || d.Kind == "upnp.router.restored" {
		state.OK = flag(true)
		state.Sentence = line("notif.state.upnp.routerOk", "at", st.LastRefresh.Local().Format("15:04"))
		return state
	}
	m, found := upnp.MappingByKey(st, key)
	if !found {
		state.Sentence = line("notif.state.upnp.gone")
		return state
	}
	port := strconv.Itoa(m.WantedPort)
	switch {
	case m.Active:
		since := ""
		if m.Since != nil {
			since = m.Since.Local().Format("2006-01-02 15:04")
		}
		state.OK = flag(true)
		state.Sentence = line("notif.state.upnp.active", "port", strconv.Itoa(m.ExternalPort), "since", since)
	case m.RetryAt != nil:
		state.OK = flag(false)
		state.Sentence = line("notif.state.upnp.failed", "port", port, "at", m.RetryAt.Local().Format("15:04"))
	default:
		state.OK = flag(false)
		state.Sentence = line("notif.state.upnp.failedNoRetry", "port", port)
	}
	if m.Error != "" {
		state.Facts = append(state.Facts, fact("notif.fact.answer", m.Error))
	}
	return state
}

func certificateState(domain string) *contextState {
	st := certs.GetStatus()
	state := &contextState{Link: "/cosmos-ui/config-general/https", Facts: []utils.NotificationFactView{}}
	domain = strings.TrimSpace(strings.Split(domain, ",")[0])
	for _, cert := range st.Certificates {
		if cert.Domain != domain {
			continue
		}
		until := cert.NotAfter.Local().Format("2006-01-02")
		switch {
		case cert.Running:
			state.Sentence = line("notif.state.cert.renewing", "domain", domain)
		case !cert.Present:
			state.OK = flag(false)
			state.Sentence = line("notif.state.cert.missing", "domain", domain)
		case cert.Expired:
			state.OK = flag(false)
			state.Sentence = line("notif.state.cert.expired", "domain", domain, "until", until)
		default:
			state.OK = flag(!cert.Warn)
			state.Sentence = line("notif.state.cert.valid", "domain", domain, "until", until)
		}
		if cert.Issuer != "" {
			state.Facts = append(state.Facts, fact("notif.fact.issuer", cert.Issuer))
		}
		if !cert.RenewAt.IsZero() {
			state.Facts = append(state.Facts, fact("notif.fact.renewAt", cert.RenewAt.Local().Format("2006-01-02")))
		}
		if cert.Last != nil {
			result := "ok"
			if !cert.Last.OK {
				result = certs.FailureSentence(cert.Last.Code)
			}
			state.Facts = append(state.Facts, fact("notif.fact.lastAttempt", cert.Last.At.Local().Format("2006-01-02 15:04")+": "+result))
		}
		if cert.Wait != nil && !cert.Wait.Until.IsZero() {
			state.Facts = append(state.Facts, fact("notif.fact.nextTry", cert.Wait.Until.Local().Format("2006-01-02 15:04")))
		}
		return state
	}
	if domain == "" {
		return nil
	}
	state.Sentence = line("notif.state.cert.unknown", "domain", domain)
	return state
}

var containerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func containerState(name string) *contextState {
	if !containerNamePattern.MatchString(name) {
		return nil
	}
	state := &contextState{Link: "/cosmos-ui/servapps/containers/" + name, Facts: []utils.NotificationFactView{}}
	if docker.Connect() != nil {
		state.Sentence = line("notif.state.docker.unavailable")
		return state
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout)
	defer cancel()
	c, err := docker.DockerClient.ContainerInspect(ctx, name)
	if err != nil {
		state.Sentence = line("notif.state.container.missing", "name", name)
		return state
	}
	if c.Config != nil {
		state.Facts = append(state.Facts, fact("notif.fact.image", c.Config.Image))
	}
	state.Facts = append(state.Facts, fact("notif.fact.restarts", strconv.Itoa(c.RestartCount)))
	if c.State == nil {
		return state
	}
	started := ""
	if t, err := time.Parse(time.RFC3339Nano, c.State.StartedAt); err == nil && !t.IsZero() && t.Year() > 1 {
		started = t.Local().Format("2006-01-02 15:04")
	}
	switch {
	case c.State.Restarting:
		state.OK = flag(false)
		state.Sentence = line("notif.state.container.restarting", "name", name, "code", strconv.Itoa(c.State.ExitCode))
	case c.State.Running:
		state.OK = flag(true)
		state.Sentence = line("notif.state.container.running", "name", name, "started", started)
	default:
		state.OK = flag(false)
		state.Sentence = line("notif.state.container.stopped", "name", name, "code", strconv.Itoa(c.State.ExitCode))
	}
	return state
}

func leftoverState(d utils.NotificationDetails) *contextState {
	state := &contextState{Link: "/cosmos-ui/servapps", Facts: []utils.NotificationFactView{}}
	name := ""
	for _, f := range d.Facts {
		if f.Key == "notif.fact.kept" {
			name = f.Value
		}
	}
	found, err := docker.ListLeftovers()
	if err != nil {
		state.Sentence = line("notif.state.docker.unavailable")
		return state
	}
	for _, entry := range found {
		if entry.Name == name {
			state.OK = flag(false)
			state.Sentence = line("notif.state.leftover.there", "name", name)
			return state
		}
	}
	state.OK = flag(true)
	state.Sentence = line("notif.state.leftover.gone", "name", name)
	return state
}

func snapshotsState() *contextState {
	st := snapshots.GetStatus()
	state := &contextState{Link: "/cosmos-ui/config-general/general", Facts: []utils.NotificationFactView{}}
	if st.Running {
		state.Sentence = line("notif.state.snapshots.running")
		return state
	}
	ok, failed := 0, 0
	for source, result := range st.LastResults {
		if result.Errors > 0 || result.Error != "" {
			failed++
			state.Facts = append(state.Facts, fact("notif.fact.folder", source+": "+firstNonEmpty(result.Error, strconv.Itoa(result.Errors)+" errors")))
		} else {
			ok++
		}
	}
	if ok+failed == 0 {
		state.Sentence = line("notif.state.snapshots.none")
		return state
	}
	state.OK = flag(failed == 0)
	state.Sentence = line("notif.state.snapshots.last", "ok", strconv.Itoa(ok), "failed", strconv.Itoa(failed))
	return state
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// diskState reads the use of a disk the system knows as a mount point;
// nothing else is looked at.
func diskState(mount string) *contextState {
	state := &contextState{Link: "/cosmos-ui/storage", Facts: []utils.NotificationFactView{}}
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	for _, p := range partitions {
		if p.Mountpoint != mount {
			continue
		}
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			return nil
		}
		percent := strconv.Itoa(int(usage.UsedPercent + 0.5))
		state.OK = flag(usage.UsedPercent < 90)
		state.Sentence = line("notif.state.disk.use", "disk", mount, "percent", percent, "free", humanBytes(usage.Free))
		state.Facts = append(state.Facts, fact("notif.fact.device", p.Device))
		return state
	}
	state.Sentence = line("notif.state.disk.unknown", "disk", mount)
	return state
}

func humanBytes(n uint64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	value := float64(n)
	i := 0
	for value >= 1000 && i < len(units)-1 {
		value /= 1000
		i++
	}
	if i == 0 {
		return strconv.FormatUint(n, 10) + " B"
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + units[i]
}

func fail2banState() *contextState {
	st := fail2ban.GetStatus("")
	state := &contextState{Link: "/cosmos-ui/security", Facts: []utils.NotificationFactView{}}
	switch {
	case !st.Installed:
		state.Sentence = line("notif.state.fail2ban.notInstalled")
	case st.Running:
		state.OK = flag(true)
		state.Sentence = line("notif.state.fail2ban.running", "jails", strconv.Itoa(len(st.Jails)))
	default:
		state.OK = flag(false)
		state.Sentence = line("notif.state.fail2ban.down")
	}
	return state
}

func telegramState() *contextState {
	st := telegram.CurrentStatus()
	state := &contextState{Link: "/cosmos-ui/config-general/general", Facts: []utils.NotificationFactView{}}
	switch {
	case !st.Enabled:
		state.Sentence = line("notif.state.telegram.off")
	case st.LastError != "":
		state.OK = flag(false)
		state.Sentence = line("notif.state.telegram.failing", "reason", utils.MaskNotificationText(st.LastError))
	case st.LastSent != nil:
		state.OK = flag(true)
		state.Sentence = line("notif.state.telegram.ok", "at", st.LastSent.Local().Format("2006-01-02 15:04"))
	default:
		state.Sentence = line("notif.state.telegram.nothingYet")
	}
	return state
}

// --- the security record and the events around it

func relatedSecurity(d utils.NotificationDetails) contextSecurity {
	out := contextSecurity{Events: []security.Event{}}
	if d.Security == nil || (len(d.Security.Kinds) == 0 && d.Security.Target == "" && d.Security.IP == "") {
		return out
	}
	from, to := window(d, time.Hour)
	filter := security.Filter{From: from, To: to, Kinds: d.Security.Kinds, Target: d.Security.Target, IP: d.Security.IP}
	page, err := security.List(filter, 0, contextSecurityMax)
	if err != nil && d.Security.Target != "" {
		// the target as the record writes it may differ: the kinds alone
		filter.Target = ""
		page, err = security.List(filter, 0, contextSecurityMax)
	}
	if err != nil {
		return out
	}
	out.Available = true
	out.Events = page.Events
	return out
}

// eventSearch is what the events of a source contain.
func eventSearch(d utils.NotificationDetails) string {
	switch d.Source {
	case "containers":
		if d.Subject != nil && containerNamePattern.MatchString(d.Subject.Name) {
			return d.Subject.Name
		}
		return "cosmos.docker"
	case "certificates":
		return "cosmos.proxy.certificate"
	case "jobs", "storage", "backup":
		return "cosmos.cron"
	}
	return "cosmos.error"
}

func relatedEvents(d utils.NotificationDetails) contextEvents {
	out := contextEvents{Events: []utils.Event{}}
	from, to := window(d, 10*time.Minute)
	events, _, err := utils.QueryEvents(utils.EventQuery{From: from, To: to, Search: eventSearch(d), Limit: contextEventsMax})
	if err != nil {
		return out
	}
	out.Available = true
	for _, e := range events {
		// the events are shown as text; their data can quote anything
		for k, v := range e.Data {
			if s, ok := v.(string); ok {
				e.Data[k] = utils.MaskNotificationText(s)
			}
		}
		out.Events = append(out.Events, e)
	}
	return out
}

// --- the log

// logMarkers are what the log lines of a source contain.
var logMarkers = map[string][]string{
	"upnp":         {"UPnP"},
	"certificates": {"certificate", "Certificate", "Let's Encrypt", "ACME", "Zones:"},
	"containers":   {"Leftovers -", "CheckUpdatesAvailable", "Container", "container"},
	"snapshots":    {"Snapshots", "snapshot"},
	"fail2ban":     {"Fail2ban"},
	"telegram":     {"Telegram"},
	"monitoring":   {"Alert", "alert", "Disk"},
	"updates":      {"Update"},
	"backup":       {"Backup"},
	"jobs":         {"CRON", "Cron"},
	"storage":      {"SnapRAID", "snapraid"},
	"cloudflare":   {"Cloudflare", "cloudflare"},
}

var logTime = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(\w+)\]`)

// logAround reads the log lines of the notification's moments that belong
// to its source, from the end of the log file (the file upstream's log
// viewer serves; the path is fixed).
func logAround(d utils.NotificationDetails, path string) contextLog {
	out := contextLog{Lines: []string{}}
	file, err := os.Open(path)
	if err != nil {
		out.Reason = line("notif.log.unreadable")
		return out
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > contextLogTail {
		file.Seek(info.Size()-contextLogTail, io.SeekStart)
	}

	moments := append([]time.Time{}, d.Seen...)
	if len(moments) == 0 {
		moments = []time.Time{d.Date}
	}
	if len(moments) > 3 {
		moments = moments[len(moments)-3:]
	}
	if d.Resolved != nil {
		moments = append(moments, *d.Resolved)
	}
	markers := logMarkers[d.Source]
	if d.Subject != nil && d.Subject.Name != "" && d.Source == "containers" {
		markers = append(markers, d.Subject.Name)
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 64<<10)
	for scanner.Scan() {
		text := scanner.Text()
		m := logTime.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		at, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local)
		if err != nil {
			continue
		}
		near := false
		for _, moment := range moments {
			if at.After(moment.Add(-contextLogWindow)) && at.Before(moment.Add(contextLogWindow)) {
				near = true
				break
			}
		}
		if !near {
			continue
		}
		if !belongs(text, m[2], markers) {
			continue
		}
		text = stripColours(text)
		if len(text) > contextLogLineLen {
			text = strings.ToValidUTF8(text[:contextLogLineLen], "") + "…"
		}
		out.Lines = append(out.Lines, utils.MaskNotificationText(text))
		if len(out.Lines) > contextLogLines {
			out.Lines = out.Lines[1:]
			out.Truncated = true
		}
	}
	out.Available = true
	if len(out.Lines) == 0 {
		out.Reason = line("notif.log.nothing")
	}
	return out
}

func belongs(text string, level string, markers []string) bool {
	if len(markers) == 0 {
		return level == "ERROR" || level == "WARN"
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

var colourCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripColours(text string) string {
	return colourCodes.ReplaceAllString(text, "")
}
