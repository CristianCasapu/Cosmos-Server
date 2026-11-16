package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// multiplexed builds Docker's log stream for a container without a terminal.
func multiplexed(frames ...string) []byte {
	var out bytes.Buffer
	for i, f := range frames {
		header := make([]byte, 8)
		header[0] = byte(1 + i%2)
		binary.BigEndian.PutUint32(header[4:], uint32(len(f)))
		out.Write(header)
		out.WriteString(f)
	}
	return out.Bytes()
}

func exited(code int, restarting bool, env ...string) types.ContainerJSON {
	return types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{Name: "/postgresql", RestartCount: 3,
			State:      &types.ContainerState{Status: "restarting", Restarting: restarting, ExitCode: code},
			HostConfig: &container.HostConfig{}},
		Config: &container.Config{Image: "bitnami/postgresql:latest", Env: env},
	}
}

// The sentences real images print when a setting is missing (testdata/startlogs),
// and what is proposed for each: never the switch that allows an empty password.
func TestRecognisedNeeds(t *testing.T) {
	cases := map[string][]string{
		"bitnami-postgresql.txt": {"POSTGRESQL_PASSWORD"},
		"postgres-official.txt":  {"POSTGRES_PASSWORD"},
		"mariadb-official.txt":   {"MARIADB_ROOT_PASSWORD"},
		"mysql-official.txt":     {"MYSQL_ROOT_PASSWORD"},
		"generic-required.txt":   {"SECRET_KEY_BASE"},
		"no-variable.txt":        {},
		"permission.txt":         {},
		"percona-server.txt":     {"MYSQL_ROOT_PASSWORD"},
		"pgadmin4.txt":           {"PGADMIN_DEFAULT_EMAIL", "PGADMIN_DEFAULT_PASSWORD"},
		"opensearch.txt":         {"OPENSEARCH_INITIAL_ADMIN_PASSWORD"},
		"bitnami-mongodb.txt":    {"MONGODB_ROOT_PASSWORD"},
	}
	for file, want := range cases {
		data, err := os.ReadFile("testdata/startlogs/" + file)
		if err != nil {
			t.Fatal(err)
		}
		r := reportFrom(exited(1, true), data, false, nil)
		got := []string{}
		for _, n := range r.Needs {
			got = append(got, n.Name)
			if !n.Secret && strings.Contains(n.Name, "PASSWORD") {
				t.Errorf("%s: %s not a secret", file, n.Name)
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: needs %v, want %v", file, got, want)
		}
		for _, l := range r.Log {
			if strings.ContainsAny(l, "\x1b\r") {
				t.Errorf("%s: control characters left in %q", file, l)
			}
		}
	}
}

// The reason in one word, from the state and the log.
func TestStartReasons(t *testing.T) {
	read := func(file string) []byte { d, _ := os.ReadFile("testdata/startlogs/" + file); return d }
	if r := reportFrom(exited(1, true), read("bitnami-postgresql.txt"), false, nil); r.Reason != "missingSetting" || !r.Failing {
		t.Errorf("%+v", r.Reason)
	}
	if r := reportFrom(exited(1, false), read("permission.txt"), false, nil); r.Reason != "permission" {
		t.Errorf("%v", r.Reason)
	}
	if r := reportFrom(exited(132, false), nil, false, nil); r.Reason != "processor" {
		t.Errorf("%v", r.Reason)
	}
	if r := reportFrom(exited(1, true), read("no-variable.txt"), false, nil); r.Reason != "restarting" {
		t.Errorf("%v", r.Reason)
	}
	// a variable that is set is not missing, even if an old line said so
	if r := reportFrom(exited(1, true, "POSTGRESQL_PASSWORD=s3cret-value"), read("bitnami-postgresql.txt"), false, nil); len(r.Needs) != 0 {
		t.Errorf("%+v", r.Needs)
	}
	// a container that runs well is not failing and nothing is recognised
	ok := exited(0, false)
	ok.State = &types.ContainerState{Status: "running", Running: true, StartedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}
	ok.RestartCount = 0
	if r := reportFrom(ok, read("bitnami-postgresql.txt"), false, nil); r.Failing || len(r.Needs) != 0 || r.Reason != "" {
		t.Errorf("%+v", r)
	}
}

// The log is shown cleaned: colours gone, secrets masked (the container's own
// and the request's), each line cut, at most startLogLines lines, the
// stream's frames in order.
func TestStartLogIsCleanedAndMasked(t *testing.T) {
	secret := "Tr0ub4dor&3-horse"
	long := strings.Repeat("x", 2000)
	frames := []string{"\x1b[38;5;1mERROR\x1b[0m ==> bad password " + secret + "\n", "second " + long + "\n"}
	for i := 0; i < 100; i++ {
		frames = append(frames, "line\n")
	}
	frames = append(frames, "key=other-secret-9\n")
	text, _ := demuxTail(bytes.NewReader(multiplexed(frames...)), false)
	r := reportFrom(exited(1, true, "APP_PASSWORD="+secret, "TZ=UTC"), text, false, []string{"API_TOKEN=other-secret-9"})
	all := strings.Join(r.Log, "\n")
	if strings.Contains(all, secret) || strings.Contains(all, "other-secret-9") {
		t.Fatalf("a secret is shown: %q", all)
	}
	if len(r.Log) != startLogLines || !r.LogCut {
		t.Fatalf("%d lines, cut %v", len(r.Log), r.LogCut)
	}
	shortText, _ := demuxTail(bytes.NewReader(multiplexed(frames[:2]...)), false)
	short := reportFrom(exited(1, true, "APP_PASSWORD="+secret), shortText, false, nil)
	if short.Log[0] != "ERROR ==> bad password <hidden>" || len(short.Log[1]) > startLineMax+4 {
		t.Fatalf("%q", short.Log)
	}
}

// Hostile logs: a name must be a variable's name; at most startNeedsMax.
func TestNeedsAreBounded(t *testing.T) {
	lines := []string{"the <script> environment variable is empty or not set", "ERROR is required", "lower_case is required"}
	for i := 0; i < 50; i++ {
		lines = append(lines, "VAR_"+strings.Repeat("A", i%5+1)+string(rune('A'+i%26))+"_X"+strings.Repeat("Y", i/26)+" is required")
	}
	needs := recogniseNeeds(lines, nil)
	if len(needs) != startNeedsMax {
		t.Fatalf("%d needs", len(needs))
	}
	for _, n := range needs {
		if !envName.MatchString(n.Name) || n.Name == "ERROR" {
			t.Errorf("%q", n.Name)
		}
	}
}

// The watch: a container that restarts is reported with its reason, one
// that runs is not, and the result is one line after the creation's log.
func TestWatchCreationReportsTheOneThatDoesNotStart(t *testing.T) {
	prevTick, prevInspect, prevNotify, prevLog := startWatchTick, inspectForWatch, notifyStartFailed, readStartLog
	defer func() {
		startWatchTick, inspectForWatch, notifyStartFailed, readStartLog = prevTick, prevInspect, prevNotify, prevLog
	}()
	startWatchTick = 10 * time.Millisecond
	calls := 0
	inspectForWatch = func(ctx context.Context, name string) (types.ContainerJSON, error) {
		calls++
		if name == "postgresql" {
			if calls < 3 {
				c := exited(0, false)
				c.State = &types.ContainerState{Status: "running", Running: true, StartedAt: time.Now().Format(time.RFC3339Nano)}
				c.RestartCount = 0
				return c, nil
			}
			return exited(1, true), nil
		}
		if name == "gone" {
			return types.ContainerJSON{}, errors.New("No such container: gone")
		}
		c := exited(0, false)
		c.State = &types.ContainerState{Status: "running", Running: true, StartedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}
		c.RestartCount = 0
		return c, nil
	}
	told := []string{}
	notifyStartFailed = func(r StartReport) { told = append(told, r.Name) }
	reported := []string{}
	prevReport := startReportOf
	defer func() { startReportOf = prevReport }()
	startReportOf = func(ctx context.Context, name string, secrets []string) (StartReport, error) {
		reported = append(reported, name)
		data, _ := os.ReadFile("testdata/startlogs/bitnami-postgresql.txt")
		return reportFrom(exited(1, true), data, false, secrets), nil
	}
	out := []string{}
	request := DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{
		"postgresql": {Name: "postgresql", Environment: []string{"TZ=UTC"}},
		"web":        {},
	}}
	check := WatchCreation(context.Background(), request, func(s string) { out = append(out, s) })
	if len(check.Containers) != 1 || check.Containers[0].Reason != "missingSetting" || check.Containers[0].Needs[0].Name != "POSTGRESQL_PASSWORD" {
		t.Fatalf("%+v", check)
	}
	if strings.Join(told, ",") != "postgresql" || strings.Join(reported, ",") != "postgresql" {
		t.Fatalf("told %v reported %v", told, reported)
	}
	last := out[len(out)-1]
	if !strings.HasPrefix(last, StartCheckMarker) || !strings.HasSuffix(last, "\n") || strings.Count(last, "\n") != 1 {
		t.Fatalf("%q", last)
	}
	var back StartCheck
	if json.Unmarshal([]byte(strings.TrimPrefix(last, StartCheckMarker)), &back) != nil || len(back.Containers) != 1 {
		t.Fatalf("%q", last)
	}
	if !strings.Contains(strings.Join(out, ""), "web runs.") {
		t.Fatalf("%q", out)
	}
}

// The watch ends when the visitor goes and never waits longer than startWatchFor.
func TestWatchCreationIsBounded(t *testing.T) {
	prevTick, prevInspect := startWatchTick, inspectForWatch
	defer func() { startWatchTick, inspectForWatch = prevTick, prevInspect }()
	startWatchTick = 10 * time.Millisecond
	inspectForWatch = func(ctx context.Context, name string) (types.ContainerJSON, error) {
		c := exited(0, false)
		c.State = &types.ContainerState{Status: "running", Running: true, StartedAt: time.Now().Format(time.RFC3339Nano), Health: &types.Health{Status: "starting"}}
		c.RestartCount = 0
		return c, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	begin := time.Now()
	check := WatchCreation(ctx, DockerServiceCreateRequest{Services: map[string]ContainerCreateRequestContainer{"a": {}}}, func(string) {})
	if time.Since(begin) > 2*time.Second || len(check.Containers) != 0 {
		t.Fatalf("%v %+v", time.Since(begin), check)
	}
	many := map[string]ContainerCreateRequestContainer{}
	for i := 0; i < 20; i++ {
		many[string(rune('a'+i))] = ContainerCreateRequestContainer{}
	}
	if len(watchNames(DockerServiceCreateRequest{Services: many})) != startWatchMax {
		t.Fatal("unbounded")
	}
}

// The port check: what real services answer to a GET, and that a port that
// speaks web says so. Each answer is a listener of the test's own.
func TestProbeAnswers(t *testing.T) {
	serve := func(reply []byte, wait bool) string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					buf := make([]byte, 256)
					c.Read(buf)
					if wait {
						time.Sleep(probeWait + time.Second)
						return
					}
					c.Write(reply)
				}(c)
			}
		}()
		return l.Addr().String()
	}
	mysql := []byte{0x4a, 0, 0, 0, 0x0a, '8', '.', '4', 0}
	cases := []struct {
		reply    []byte
		wait     bool
		answer   string
		protocol string
	}{
		{[]byte("HTTP/1.1 200 OK\r\nContent-Type: text/html\r\n\r\n<html>"), false, "http", ""},
		{[]byte("HTTP/1.0 200 OK\r\n\r\nIt looks like you are trying to access MongoDB over HTTP on the native driver port.\r\n"), false, "other", "mongodb"},
		{[]byte("-NOAUTH Authentication required.\r\n"), false, "other", "redis"},
		{[]byte("SSH-2.0-OpenSSH_9.6\r\n"), false, "other", "ssh"},
		{mysql, false, "other", "mysql"},
		{[]byte{}, false, "closed", ""},
		{nil, true, "silent", ""},
	}
	for _, c := range cases {
		got := askPort(context.Background(), serve(c.reply, c.wait), "app")
		if got.Answer != c.answer || got.Protocol != c.protocol {
			t.Errorf("%q: %+v", c.reply, got)
		}
		if strings.ContainsAny(got.First, "\r\n<") && got.Answer == "http" && strings.Contains(got.First, "<html>") {
			t.Errorf("more than the first line: %q", got.First)
		}
	}
	// nothing listens
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	if got := askPort(context.Background(), addr, "app"); got.Answer != "refused" {
		t.Errorf("%+v", got)
	}
}

// The port check never asks a container on the host's network, nor one that
// is not running: the address is Docker's, never the request's.
func TestProbeOnlyAsksTheContainersOwnAddress(t *testing.T) {
	prevAddr, prevDial := probeAddress, probeDial
	defer func() { probeAddress, probeDial = prevAddr, prevDial }()
	dialed := []string{}
	probeDial = func(ctx context.Context, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("refused")
	}
	probeAddress = func(ctx context.Context, name string) (string, string) {
		switch name {
		case "hostapp":
			return "", "hostNetwork"
		case "down":
			return "", "notRunning"
		}
		return "172.17.0.9", ""
	}
	if r := ProbeContainerPort(context.Background(), "hostapp", 80); r.Answer != "hostNetwork" {
		t.Error(r)
	}
	if r := ProbeContainerPort(context.Background(), "down", 80); r.Answer != "notRunning" {
		t.Error(r)
	}
	ProbeContainerPort(context.Background(), "db", 5432)
	if strings.Join(dialed, ",") != "172.17.0.9:5432" {
		t.Fatalf("dialed %v", dialed)
	}
	for i := 0; i < probePerMin; i++ {
		if !probeAllowed("user:test", time.Now()) {
			t.Fatal("refused too early")
		}
	}
	if probeAllowed("user:test", time.Now()) {
		t.Fatal("unbounded")
	}
}

// Review B3: masked before the cut (a secret at the 400-byte mark), inside
// multi-line values and values with a tab, the password of an address; a
// short value is not masked (masking every "postgres" would tell the
// password: values under maskMinLength); bidirectional overrides removed.
func TestMaskingBeforeTheCut(t *testing.T) {
	secret := "LongSecretValue-1234567890"
	padding := strings.Repeat("x", startLineMax-10)
	multi := "line-one-of-the-key\nline-two-of-the-key"
	tabbed := "tab\tseparated-secret"
	log := padding + secret + "\n" +
		"key: line-two-of-the-key\n" +
		"got tab\tseparated-secret\n" +
		"connecting to postgres://app:pw@db:5432/app\n" +
		"user example connected\n" +
		"evil \u202emirrored\n"
	r := reportFrom(exited(1, true, "APP_PASSWORD="+secret, "TLS_PRIVATE_KEY="+multi, "SOME_TOKEN="+tabbed, "DATABASE_URL=postgres://app:pw@db:5432/app", "POSTGRES_PASSWORD=example"), []byte(log), false, nil)
	all := strings.Join(r.Log, "\n")
	for _, leak := range []string{"LongSecret", "line-two-of-the-key", "separated-secret", ":pw@", "\u202e"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q is shown: %q", leak, all)
		}
	}
	if !strings.Contains(all, "user example connected") {
		t.Errorf("a short value masked everywhere: %q", all)
	}
}

// Review B4: names that change how programs load or connect are never proposed.
func TestDeniedNamesAreNeverProposed(t *testing.T) {
	lines := []string{"LD_PRELOAD is required", "PATH must be set", "DOCKER_HOST is not set", "COSMOS_TOKEN is required", "HTTP_PROXY is required", "NODE_OPTIONS must be set", "APP_SECRET is required"}
	needs := recogniseNeeds(lines, nil)
	if len(needs) != 1 || needs[0].Name != "APP_SECRET" {
		t.Fatalf("%+v", needs)
	}
}

// Review B5: the END of a long log is kept, frames stay aligned, and the read is bounded.
func TestTheEndOfALongLogIsKept(t *testing.T) {
	frames := []string{}
	for i := 0; i < 3000; i++ {
		frames = append(frames, fmt.Sprintf("noise line %d %s\n", i, strings.Repeat("y", 60)))
	}
	frames = append(frames, "The LAST_VAR environment variable is empty or not set.\n")
	text, cut := demuxTail(bytes.NewReader(multiplexed(frames...)), false)
	if !cut || len(text) > startLogBytes || !strings.HasSuffix(string(text), "The LAST_VAR environment variable is empty or not set.\n") || strings.Contains(string(text), "\x00") {
		t.Fatalf("cut %v, %d bytes, ends %q", cut, len(text), string(text[len(text)-60:]))
	}
	r := reportFrom(exited(1, true), text, cut, nil)
	if len(r.Needs) != 1 || r.Needs[0].Name != "LAST_VAR" {
		t.Fatalf("%+v", r.Needs)
	}
	// an endless stream: at most startReadMax is read
	start := time.Now()
	demuxTail(io.LimitReader(zeroFrames{}, 64<<20), false)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("%v", time.Since(start))
	}
}

type zeroFrames struct{}

func (zeroFrames) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// Review B5: one notification per container and kind of failure; the start
// check's silence on "keeps stopping" is short, and a later failure is told.
func TestStartFailureNotificationsAreNotRepeated(t *testing.T) {
	now := time.Now()
	if !startFailedNew("pgx", "missingSetting", now) || startFailedNew("pgx", "missingSetting", now.Add(time.Minute)) {
		t.Fatal("the same failure twice")
	}
	if !startFailedNew("pgx", "permission", now.Add(2*time.Minute)) || !startFailedNew("pgx", "missingSetting", now.Add(2*time.Hour)) {
		t.Fatal("another failure, or the same much later, is told")
	}
	dyingMu.Lock()
	delete(dyingContainers, "pgy")
	dyingMu.Unlock()
	markDyingTold("pgy", now)
	told := false
	for i := 0; i < dyingExits; i++ {
		if ok, _ := containerDied("pgy", "die", "1", now.Add(time.Duration(i)*time.Minute)); ok {
			told = true
		}
	}
	if told {
		t.Fatal("told again minutes after the start check")
	}
	for i := 0; i < dyingExits; i++ {
		if ok, _ := containerDied("pgy", "die", "1", now.Add(40*time.Minute+time.Duration(i)*time.Minute)); ok {
			told = true
		}
	}
	if !told {
		t.Fatal("a failure 40 minutes later is not told")
	}
}

// Review C1: the port check never asks the server itself: not a container on
// the host's network, nor one that shares the network of such a container.
func TestProbeNeverAsksTheServerItself(t *testing.T) {
	prev := inspectForTarget
	defer func() { inspectForTarget = prev }()
	inspectForTarget = func(name string) (types.ContainerJSON, error) {
		return inspected(types.ContainerState{Running: true}, "host", nil), nil
	}
	sidecar := inspected(types.ContainerState{Running: true}, "container:hostapp", nil)
	if ip, why := probeAddressOf(sidecar); ip != "" || why != "hostNetwork" {
		t.Fatalf("container: mode of a host-network container: %q %q", ip, why)
	}
	if ip, why := probeAddressOf(inspected(types.ContainerState{Running: true}, "host", nil)); ip != "" || why != "hostNetwork" {
		t.Fatalf("host mode: %q %q", ip, why)
	}
	if ip, _ := probeAddressOf(inspected(types.ContainerState{Running: true}, "bridge", map[string]string{"bridge": "172.17.0.4"})); ip != "172.17.0.4" {
		t.Fatalf("bridge: %q", ip)
	}
	// the whole server's bound
	for i := 0; i < probeServerPerMin; i++ {
		if !probeAllowed("\x00server", time.Now()) {
			t.Fatalf("refused at %d", i)
		}
	}
	if probeAllowed("\x00server", time.Now()) {
		t.Fatal("unbounded for the server")
	}
}
