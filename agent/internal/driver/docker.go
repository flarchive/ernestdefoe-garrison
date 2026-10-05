package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ernestdefoe/garrison/internal/protocol"
)

// Docker supervises servers that run as containers.
//
// It drives the `docker` CLI with JSON output formats rather than the Engine
// API over the socket. For phase 0 that is the right trade: no dependency, it
// works identically with podman's docker-compatible CLI, and the CLI is the
// thing an operator has already proved works on their host. Phase 1 should
// move to the socket for the streaming verbs, where the CLI costs a process
// per subscriber.
//
// 🚨 Note what this file does NOT do: it never accepts a command, an image or
// a flag from the request. Everything it runs is assembled here from a Server
// the OPERATOR configured. That is what keeps `docker` a privileged tool the
// agent uses rather than one the forum can reach.
type Docker struct{}

func NewDocker() *Docker { return &Docker{} }

func (d *Docker) Name() string { return "docker" }

func (d *Docker) Available(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		return fmt.Errorf("docker CLI not usable: %w", err)
	}
	return nil
}

func (d *Docker) container(s Server) (string, error) {
	if s.Container == "" {
		return "", protocol.Errf(protocol.CodeBadRequest, "server %q has no container configured", s.ID)
	}
	return s.Container, nil
}

// inspect is the shape we read out of `docker inspect`. Only the fields we
// use — the full document is enormous and unstable.
type inspect struct {
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Pid        int    `json:"Pid"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func (d *Docker) Status(ctx context.Context, s Server) (protocol.Status, error) {
	name, err := d.container(s)
	if err != nil {
		return protocol.Status{}, err
	}
	st := protocol.Status{Server: s.ID, Driver: d.Name(), State: protocol.StateUnknown}

	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .}}", name).Output()
	if err != nil {
		// A container that does not exist is a configuration fact, not a
		// crash. Say which, or the operator goes looking for the wrong thing.
		st.State = protocol.StateStopped
		st.Detail = "container not found"
		return st, nil
	}

	var in inspect
	if err := json.Unmarshal(out, &in); err != nil {
		return st, protocol.Errf(protocol.CodeDriverFailed, "parsing docker inspect: %v", err)
	}

	st.PID = in.State.Pid
	// Docker reports "0001-01-01T00:00:00Z" for a container that has never
	// been started — parseable, and meaningless.
	if t, err := time.Parse(time.RFC3339Nano, in.State.StartedAt); err == nil && !t.IsZero() {
		st.Since = &t
	}

	// 🚨 Normalise. Docker's vocabulary is its own — "exited", "created",
	// "restarting", "dead" — and letting any of it reach the forum means every
	// consumer has to learn one driver's dialect.
	switch in.State.Status {
	case "running":
		st.State = protocol.StateRunning
	case "restarting":
		st.State = protocol.StateStarting
	case "removing":
		st.State = protocol.StateStopping
	case "exited", "dead":
		if in.State.ExitCode != 0 {
			st.State = protocol.StateCrashed
			st.Detail = fmt.Sprintf("exited with status %d", in.State.ExitCode)
		} else {
			st.State = protocol.StateStopped
		}
	case "created", "paused":
		st.State = protocol.StateStopped
		st.Detail = in.State.Status
	}

	if in.State.Health != nil {
		healthy := in.State.Health.Status == "healthy"
		st.Healthy = &healthy
		if st.Detail == "" {
			st.Detail = "health: " + in.State.Health.Status
		}
	}
	return st, nil
}

func (d *Docker) Start(ctx context.Context, s Server) error {
	name, err := d.container(s)
	if err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "docker", "start", name).CombinedOutput(); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "docker start: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop uses docker's own grace period rather than reimplementing one.
//
// `docker stop -t N` sends SIGTERM, waits N seconds, then SIGKILLs — exactly
// the ladder the process driver builds by hand. Same verb, same promise, two
// completely different mechanisms underneath, which is the whole point of the
// driver interface.
func (d *Docker) Stop(ctx context.Context, s Server, grace time.Duration) error {
	name, err := d.container(s)
	if err != nil {
		return err
	}

	// Ask the game first where the operator told us how, exactly as the
	// process driver does.
	if s.StopCommand != "" {
		_ = d.Send(ctx, s, s.StopCommand)
	}

	secs := int(grace.Seconds())
	if secs < 1 {
		secs = 1
	}
	// The context needs to outlive the grace period or we kill the client
	// mid-wait and report a timeout for a stop that was working.
	ctx, cancel := context.WithTimeout(ctx, grace+30*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "docker", "stop", "-t", strconv.Itoa(secs), name).CombinedOutput(); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "docker stop: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

type dockerStats struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	PIDs     string `json:"PIDs"`
}

func (d *Docker) Stats(ctx context.Context, s Server) (protocol.Stats, error) {
	name, err := d.container(s)
	if err != nil {
		return protocol.Stats{}, err
	}
	out := protocol.Stats{Server: s.ID, At: time.Now().UTC(), Source: "docker"}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	b, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}", name).Output()
	if err != nil {
		return out, protocol.Errf(protocol.CodeDriverFailed, "docker stats: %v", err)
	}
	var ds dockerStats
	if err := json.Unmarshal(b, &ds); err != nil {
		return out, protocol.Errf(protocol.CodeDriverFailed, "parsing docker stats: %v", err)
	}

	out.CPUPercent = parsePercent(ds.CPUPerc)
	used, limit := parseMemUsage(ds.MemUsage)
	out.MemoryBytes = used
	out.MemoryLimit = limit
	if n, err := strconv.Atoi(strings.TrimSpace(ds.PIDs)); err == nil {
		out.Processes = n
	}
	return out, nil
}

func parsePercent(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	return v
}

// parseMemUsage reads docker's "1.375GiB / 125GiB" into bytes.
func parseMemUsage(s string) (used, limit uint64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseSize(parts[0]), parseSize(parts[1])
}

func parseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		mult   float64
	}{
		{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if rest, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			if err != nil {
				return 0
			}
			return uint64(v * u.mult)
		}
	}
	return 0
}

func (d *Docker) Tail(ctx context.Context, s Server, history int, follow bool, sink func(protocol.Line)) error {
	name, err := d.container(s)
	if err != nil {
		return err
	}

	args := []string{"logs", "--timestamps", "--tail", strconv.Itoa(max(history, 0))}
	if follow {
		args = append(args, "--follow")
	}
	args = append(args, name)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "docker logs: %v", err)
	}
	// 🚨 Containers write plenty to stderr and it is not an error — a Java
	// server logs there by default. Merging both keeps the console honest.
	cmd.Stderr = cmd.Stdout
	stderrPipe, err := cmd.StderrPipe()
	if err == nil {
		go func() {
			sc := bufio.NewScanner(stderrPipe)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				sink(parseDockerLine(s.ID, sc.Text(), true))
			}
		}()
	}

	if err := cmd.Start(); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "docker logs: %v", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		sink(parseDockerLine(s.ID, sc.Text(), false))
	}

	_ = cmd.Wait()
	return nil
}

// parseDockerLine splits docker's RFC3339 timestamp prefix off a log line so
// the forum gets a real time rather than "when the agent happened to read it".
func parseDockerLine(server, raw string, stderr bool) protocol.Line {
	l := protocol.Line{Server: server, At: time.Now().UTC(), Text: raw, Stderr: stderr}
	if i := strings.IndexByte(raw, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, raw[:i]); err == nil {
			l.At = t.UTC()
			l.Text = raw[i+1:]
		}
	}
	return l
}

// Send writes a line to the container's stdin.
//
// 🚨 `docker attach`, NOT `docker exec`. exec would run an arbitrary command
// inside the container, which is precisely the capability this whole design
// exists to withhold — and it would not reach the game's console anyway.
// attach requires the container to have been created with stdin open; when it
// was not, say so plainly rather than falling back to something more powerful.
func (d *Docker) Send(ctx context.Context, s Server, line string) error {
	name, err := d.container(s)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "attach", "--sig-proxy=false", name)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "attach: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return protocol.Errf(protocol.CodeNotSupported,
			"container %q does not accept console input (created without an open stdin)", name)
	}
	if _, err := stdin.Write([]byte(line + "\n")); err != nil {
		_ = cmd.Process.Kill()
		return protocol.Errf(protocol.CodeDriverFailed, "writing to console: %v", err)
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill() // detach; attach has no other way out
	_ = cmd.Wait()
	return nil
}

var _ Driver = (*Docker)(nil)
