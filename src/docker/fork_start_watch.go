package docker

// Fork addition (plan 43): after a creation (any image, the Market, the
// compose import, the new-container form: all of them POST
// /api/docker-service), the new containers are watched for a short while.
// One that stops with an error, restarts or turns unhealthy is reported in
// the creation's own answer, after "[OPERATION SUCCEEDED]", as one line
//
//   [START CHECK] {"containers":[StartReport...]}
//
// which the panel turns into words and, when it recognises the reason, a
// repair. The administrators are told once by a notification (Telegram
// follows the notification kinds). Bounded: startWatchFor at most, one
// inspect per container and second, startWatchMax containers, the log as
// StartReport bounds it.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/azukaar/cosmos-server/src/utils"
	"github.com/docker/docker/api/types"
)

const (
	startWatchFor    = 30 * time.Second
	startStableFor   = 12 * time.Second
	startWatchMax    = 8
	StartCheckMarker = "[START CHECK] "
)

// StartCheck is the line written after a creation.
type StartCheck struct {
	Containers []StartReport `json:"containers"`
	// Watched: the seconds the containers were watched
	Watched int `json:"watched"`
}

// startWatchTick and inspectForWatch are variables so that tests run fast and answer for Docker.
var (
	startWatchTick  = time.Second
	inspectForWatch = func(ctx context.Context, name string) (types.ContainerJSON, error) {
		return DockerClient.ContainerInspect(ctx, name)
	}
	startReportOf     = StartReportOf
	notifyStartFailed = func(r StartReport) {
		markDyingTold(r.Name, time.Now())
		if startFailedNew(r.Name, r.Reason, time.Now()) {
			writeStartFailedNotice(r)
		}
	}
)

// verdict says whether a container is settled: failing, fine, or not yet known.
func verdict(c types.ContainerJSON, now time.Time) (settled bool, failing bool) {
	if c.ContainerJSONBase == nil || c.State == nil {
		return true, true
	}
	s := c.State
	if s.Restarting || c.RestartCount > 0 {
		return true, true
	}
	if !s.Running {
		// a one-shot container that finished well is fine
		return s.Status != "created", s.ExitCode != 0 || s.OOMKilled
	}
	if s.Health != nil && s.Health.Status != "" && s.Health.Status != "none" {
		switch s.Health.Status {
		case "healthy":
			return true, false
		case "unhealthy":
			return true, true
		}
		return false, false
	}
	started, err := time.Parse(time.RFC3339Nano, s.StartedAt)
	return err == nil && now.Sub(started) >= startStableFor, false
}

// watchNames are the containers a creation made, by name, bounded.
func watchNames(request DockerServiceCreateRequest) []string {
	names := []string{}
	for key, service := range request.Services {
		name := service.Name
		if name == "" {
			name = key
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > startWatchMax {
		names = names[:startWatchMax]
	}
	return names
}

// requestSecrets are the values of secret variables the request sets: masked in logs too.
func requestSecrets(request DockerServiceCreateRequest) []string {
	env := []string{}
	for _, service := range request.Services {
		env = append(env, service.Environment...)
	}
	return env
}

// WatchCreation watches the containers of a creation and writes the result.
func WatchCreation(ctx context.Context, request DockerServiceCreateRequest, OnLog func(string)) StartCheck {
	names := watchNames(request)
	check := StartCheck{Containers: []StartReport{}}
	if len(names) == 0 {
		return check
	}
	OnLog(fmt.Sprintf("Checking that the new app starts (up to %d seconds)...\n", int(startWatchFor/time.Second)))
	begin := time.Now()
	pending := map[string]bool{}
	for _, n := range names {
		pending[n] = true
	}
	failing := map[string]bool{}
	for len(pending) > 0 && time.Since(begin) < startWatchFor && ctx.Err() == nil {
		for _, n := range names {
			if !pending[n] {
				continue
			}
			c, err := inspectForWatch(ctx, n)
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				delete(pending, n)
				failing[n] = true
				continue
			}
			if settled, bad := verdict(c, time.Now()); settled {
				delete(pending, n)
				failing[n] = bad
			}
		}
		if len(pending) == 0 {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(startWatchTick):
		}
	}
	check.Watched = int(time.Since(begin).Round(time.Second) / time.Second)
	secrets := requestSecrets(request)
	for _, n := range names {
		if !failing[n] {
			if pending[n] {
				OnLog(fmt.Sprintf("%s is still starting.\n", n))
			} else {
				OnLog(fmt.Sprintf("%s runs.\n", n))
			}
			continue
		}
		readCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		report, _ := startReportOf(readCtx, n, secrets)
		cancel()
		if report.Name == "" {
			report.Name = n
		}
		report.Failing = true
		if report.Reason == "" {
			report.Reason = reasonOf(&report)
		}
		OnLog(utils.DoErr("%s does not run: %s\n", n, reasonWords(report)))
		check.Containers = append(check.Containers, report)
		notifyStartFailed(report)
	}
	line, err := json.Marshal(check)
	if err == nil {
		OnLog(StartCheckMarker + string(line) + "\n")
	}
	return check
}

// reasonWords is the reason for the creation's log and the notification.
func reasonWords(r StartReport) string {
	switch r.Reason {
	case "missingSetting":
		names := ""
		for i, n := range r.Needs {
			if i > 0 {
				names += ", "
			}
			names += n.Name
		}
		return "it says it needs " + names
	case "permission":
		return "it may not write to a folder it needs"
	case "processor":
		return "this server's processor cannot run it"
	case "memory":
		return "it ran out of memory"
	case "unhealthy":
		return "its health check fails"
	case "restarting":
		return "it keeps stopping and Docker starts it again"
	case "missing":
		return "it was not found"
	}
	return fmt.Sprintf("it stopped with exit code %d", r.ExitCode)
}

// One notification per container and kind of failure: the rounds of a
// repair that fail for the same reason are not told again for an hour.
var (
	startFailedMu   sync.Mutex
	startFailedSeen = map[string]time.Time{}
)

func startFailedNew(name, reason string, now time.Time) bool {
	startFailedMu.Lock()
	defer startFailedMu.Unlock()
	key := name + "\x00" + reason
	if at, ok := startFailedSeen[key]; ok && now.Sub(at) < time.Hour {
		return false
	}
	if len(startFailedSeen) >= 256 {
		startFailedSeen = map[string]time.Time{}
	}
	startFailedSeen[key] = now
	return true
}
