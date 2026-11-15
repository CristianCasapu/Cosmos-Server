package anyimage

// Every recipe against the real image (opt-in, never the server's Docker):
//
//   COSMOS_FORK_LIVE_DOCKER=1 DOCKER_HOST=tcp://<dind>:2375 go test ./src/anyimage/ -run RecipesLive -v
//
// For each recipe Cosmos reads the image's declaration from its real registry
// (the tag it would propose, through describe: Docker Hub's API and the
// distribution API, no pull), then the Docker in Docker of the test pulls
// that image and the test compares: what the recipe says about volumes,
// ports and the user must be what the image declares (or what its
// documentation says, where the recipe says "docs"), and what Cosmos read
// without a pull must be what the daemon sees after the pull. A recipe
// cannot drift from its image silently. Pulls count against Docker Hub's
// anonymous limit (one per image).

import (
	"context"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
)

func TestRecipesLive(t *testing.T) {
	if os.Getenv("COSMOS_FORK_LIVE_DOCKER") != "1" || os.Getenv("DOCKER_HOST") == "" {
		t.Skip("set COSMOS_FORK_LIVE_DOCKER=1 and DOCKER_HOST to a Docker in Docker of the test's own")
	}
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	serverPlatform = func() Platform { return Platform{OS: "linux", Arch: "amd64"} }
	only := os.Getenv("ONLY")
	for _, recipe := range recipes.Recipes {
		if only != "" && !strings.Contains(","+only+",", ","+recipe.ID+",") {
			continue
		}
		for _, imageName := range recipe.Images {
			tags := []string{""}
			if recipe.ID == "postgres" {
				tags = []string{"", "17"} // both sides of the data folder's move
			}
			for _, tag := range tags {
				reference := imageName
				if tag != "" {
					reference += ":" + tag
				}
				t.Run(recipe.ID+"/"+reference, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
					defer cancel()
					desc := Describe(ctx, reference)
					if desc.Problem != "" || desc.Declared == nil {
						t.Fatalf("describe %s: %q", reference, desc.Problem)
					}
					full := desc.Reference
					if ref, err := Parse(reference, nil); err == nil && ref.Registry == hubRegistry {
						var repo struct {
							LastUpdated string `json:"last_updated"`
						}
						if getJSON(ctx, hubAPI+"/v2/repositories/"+ref.Repository+"/", 64<<10, &repo) == nil {
							if last, err := time.Parse(time.RFC3339, repo.LastUpdated); err == nil && time.Since(last) > 365*24*time.Hour {
								t.Errorf("%s was last published on %s: is it still maintained? (recipes.json says %s)", ref.Name(), repo.LastUpdated[:10], recipe.Maintained.LastImage)
							}
						}
					}
					t.Logf("%s: proposed tag %s (%s)", reference, desc.Tag, desc.Tags.Why)
					pull, err := docker.ImagePull(ctx, full, image.PullOptions{Platform: "linux/amd64"})
					if err != nil {
						t.Fatalf("pull %s: %v", full, err)
					}
					io.Copy(io.Discard, pull)
					pull.Close()
					inspected, _, err := docker.ImageInspectWithRaw(ctx, full)
					if err != nil {
						t.Fatal(err)
					}
					cfg := inspected.Config
					volumes := []string{}
					for v := range cfg.Volumes {
						volumes = append(volumes, v)
					}
					sort.Strings(volumes)
					ports := []string{}
					for p := range cfg.ExposedPorts {
						ports = append(ports, string(p))
					}
					sort.Strings(ports)
					// what Cosmos read without a pull is what the daemon has
					if strings.Join(volumes, ",") != strings.Join(desc.Declared.Volumes, ",") || strings.Join(ports, ",") != strings.Join(desc.Declared.Ports, ",") || cfg.User != desc.Declared.User {
						t.Errorf("read without a pull %v %v %q, the daemon %v %v %q", desc.Declared.Volumes, desc.Declared.Ports, desc.Declared.User, volumes, ports, cfg.User)
					}
					env := map[string]string{}
					for _, line := range cfg.Env {
						if i := strings.IndexByte(line, '='); i > 0 {
							env[line[:i]] = line[i+1:]
						}
					}
					major := -1
					if m := versionTag.FindStringSubmatch(strings.TrimPrefix(strings.Split(desc.Tag, "-")[0], "v")); m != nil {
						major, _ = strconv.Atoi(m[1])
					} else if v := env["PG_MAJOR"]; v != "" {
						major, _ = strconv.Atoi(v)
					}
					targets := []string{}
					for _, v := range recipe.Volumes {
						if (v.From != 0 && major >= 0 && major < v.From) || (v.Until != 0 && major >= 0 && major > v.Until) {
							continue
						}
						targets = append(targets, v.Target)
						switch {
						case v.Evidence == "declared":
							if !contains(volumes, v.Target) {
								t.Errorf("%s: the recipe says the image declares %s, it declares %v", full, v.Target, volumes)
							}
						case v.Evidence == "workdir":
							if cfg.WorkingDir != v.Target {
								t.Errorf("%s: the recipe says the work directory is %s, it is %q", full, v.Target, cfg.WorkingDir)
							}
						case strings.HasPrefix(v.Evidence, "env:"):
							name := strings.TrimPrefix(v.Evidence, "env:")
							if !strings.HasPrefix(env[name], v.Target) {
								t.Errorf("%s: the recipe says %s points to %s, it is %q", full, name, v.Target, env[name])
							}
						}
					}
					// a volume the image declares that the recipe does not keep would be an anonymous volume, lost at a recreate
					for _, declared := range volumes {
						if !contains(targets, declared) {
							t.Errorf("%s declares the volume %s, which the recipe does not mount", full, declared)
						}
					}
					// the data folder a variable names is inside a mounted folder
					for _, name := range []string{"PGDATA", "RABBITMQ_DATA_DIR"} {
						if value := env[name]; value != "" && !under(value, targets) {
							t.Errorf("%s: %s=%s is outside the mounted folders %v", full, name, value, targets)
						}
					}
					for _, port := range []*RecipePort{recipe.Service, recipe.Web} {
						if port != nil && !port.Undeclared && !contains(ports, strconv.Itoa(port.Port)+"/tcp") {
							t.Errorf("%s: the recipe's port %d is not one the image declares %v", full, port.Port, ports)
						}
					}
					for _, p := range recipe.Ports {
						if !contains(ports, strconv.Itoa(p.Port)+"/tcp") && !contains(ports, strconv.Itoa(p.Port)+"/udp") {
							t.Errorf("%s: the recipe lists port %d, the image declares %v", full, p.Port, ports)
						}
					}
					if recipe.User != "" && cfg.User != recipe.User {
						t.Errorf("%s: the recipe says it runs as %q, the image as %q", full, recipe.User, cfg.User)
					}
					if recipe.Healthcheck != nil && cfg.Healthcheck != nil && len(cfg.Healthcheck.Test) > 0 {
						t.Logf("%s now has a healthcheck of its own (%v): the recipe's is not added", full, cfg.Healthcheck.Test)
					}
					startRecipe(t, ctx, docker, recipe, full, desc.Tag, targets)
					docker.ImageRemove(ctx, full, image.RemoveOptions{Force: true, PruneChildren: true})
				})
			}
		}
	}
}

// startRecipe runs the image with the recipe's settings, its data folders
// made the way Cosmos makes them (mode 0750, owned by root unless the recipe
// runs the container as a user), and checks that it keeps running and that
// the recipe's health check passes inside it.
func startRecipe(t *testing.T, ctx context.Context, docker *client.Client, recipe Recipe, full, tag string, targets []string) {
	name := "livetest-" + recipe.ID
	docker.ContainerRemove(ctx, name, container.RemoveOptions{Force: true, RemoveVolumes: true})
	env := []string{}
	for _, e := range recipe.Env {
		value := e.Value
		if e.Type == "secret" {
			value = "Lt" + strconv.FormatInt(time.Now().UnixNano()%1e12, 36) + "x9-Q"
		}
		if e.Type != "optional" {
			env = append(env, e.Name+"="+value)
		}
	}
	runAs := runAsFor(recipe, tag)
	mounts := []mount.Mount{}
	for i, target := range targets {
		keep := false
		for _, v := range recipe.Volumes {
			keep = keep || (v.Target == target && v.KeepVolume)
		}
		if keep {
			mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: name + "-" + strconv.Itoa(i), Target: target})
			continue
		}
		folder := "/livetest/" + recipe.ID + "/" + strconv.Itoa(i)
		owner := "0:0"
		if runAs != "" {
			owner = runAs
		}
		made := helper(t, ctx, docker, "rm -rf "+folder+" && mkdir -p "+folder+" && chmod 750 "+folder+" && chown "+owner+" "+folder+" && stat -c %a:%u:%g "+folder)
		if !strings.Contains(made, "750:"+owner) {
			t.Fatalf("the data folder was not made as Cosmos makes it: %q", made)
		}
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: folder, Target: target})
	}
	for _, b := range recipe.Binds {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: b.Source, Target: b.Target, ReadOnly: b.ReadOnly})
	}
	config := &container.Config{Image: full, Env: env, User: runAs, Cmd: recipe.Command}
	if recipe.Hostname {
		config.Hostname = name
	}
	if _, err := docker.ContainerCreate(ctx, config, &container.HostConfig{Mounts: mounts}, nil, nil, name); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer docker.ContainerRemove(context.Background(), name, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if err := docker.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	healthy := recipe.Healthcheck == nil
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		state, err := docker.ContainerInspect(ctx, name)
		if err != nil || !state.State.Running || state.RestartCount > 0 {
			logs, _ := docker.ContainerLogs(ctx, name, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "15"})
			text := ""
			if logs != nil {
				data, _ := io.ReadAll(logs)
				text = string(data)
			}
			t.Fatalf("%s stopped with the recipe's settings: %s", full, text)
		}
		if !healthy {
			cmd := recipe.Healthcheck.Test[1:]
			if recipe.Healthcheck.Test[0] == "CMD-SHELL" {
				cmd = []string{"sh", "-c", strings.Join(recipe.Healthcheck.Test[1:], " ")}
			}
			exec, err := docker.ContainerExecCreate(ctx, name, types.ExecConfig{Cmd: cmd, Env: env})
			if err == nil {
				if docker.ContainerExecStart(ctx, exec.ID, types.ExecStartCheck{}) == nil {
					for i := 0; i < 20; i++ {
						result, err := docker.ContainerExecInspect(ctx, exec.ID)
						if err == nil && !result.Running {
							healthy = result.ExitCode == 0
							break
						}
						time.Sleep(500 * time.Millisecond)
					}
				}
			}
		}
		if healthy && time.Since(deadline.Add(-150*time.Second)) > 40*time.Second {
			return // healthy and still running after 40 s
		}
	}
	if !healthy {
		t.Errorf("%s: the recipe's health check never passed: %v", full, recipe.Healthcheck.Test)
	}
}

// helper runs a shell command in a busybox container with /livetest bound and answers its output.
func helper(t *testing.T, ctx context.Context, docker *client.Client, command string) string {
	if _, _, err := docker.ImageInspectWithRaw(ctx, "busybox:1.36"); err != nil {
		pull, err := docker.ImagePull(ctx, "busybox:1.36", image.PullOptions{})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, pull)
		pull.Close()
	}
	made, err := docker.ContainerCreate(ctx, &container.Config{Image: "busybox:1.36", Cmd: []string{"sh", "-c", command}},
		&container.HostConfig{Binds: []string{"/livetest:/livetest"}}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer docker.ContainerRemove(context.Background(), made.ID, container.RemoveOptions{Force: true})
	if err := docker.ContainerStart(ctx, made.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	wait, errs := docker.ContainerWait(ctx, made.ID, container.WaitConditionNotRunning)
	select {
	case <-wait:
	case err := <-errs:
		t.Fatal(err)
	}
	logs, err := docker.ContainerLogs(ctx, made.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return ""
	}
	defer logs.Close()
	data, _ := io.ReadAll(logs)
	return string(data)
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

func under(path string, dirs []string) bool {
	for _, d := range dirs {
		if path == d || strings.HasPrefix(path, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}
