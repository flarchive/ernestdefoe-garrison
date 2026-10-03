package driver

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ernestdefoe/garrison/internal/protocol"
)

// Systemd supervises servers that already run as system units.
//
// 🚨 This is the driver for a server the host is ALREADY responsible for.
// The process driver starts a binary and babysits it; this one drives a unit
// somebody has written, and the difference matters more than it sounds:
//
//   - The unit decides which user the game runs as. That is the only way to
//     run a server that refuses to be root — Unreal dedicated servers refuse
//     outright — because the agent itself runs as root and anything it spawns
//     directly inherits that.
//   - The unit owns restart policy, resource limits and sandboxing. An
//     operator who has written ProtectSystem and NoNewPrivileges does not want
//     a game panel quietly starting the same binary without them.
//   - The server keeps running across an agent restart, and comes back after a
//     reboot, whether or not the agent does.
//
// 🚨 Like the docker driver, nothing here is assembled from a request. The unit
// name comes from the operator's config and is validated before it ever reaches
// systemctl — everything else is fixed. That is what keeps systemctl a tool the
// agent uses rather than one the forum can reach.
type Systemd struct{}

func NewSystemd() *Systemd { return &Systemd{} }

func (sd *Systemd) Name() string { return "systemd" }

// unitPattern is deliberately strict.
//
// 🚨 A unit name is an argument to systemctl. It arrives from a config file
// rather than from the network, but "the input is trusted" is exactly the
// assumption that stops being true later — a template, a provisioning flow, a
// future admin field. Anything outside this set is refused rather than escaped,
// because escaping is a thing you can get subtly wrong and a character class is
// not.
var unitPattern = regexp.MustCompile(`^[A-Za-z0-9@:_.\\-]{1,128}$`)

// unit returns the validated unit name for a server.
func (sd *Systemd) unit(s Server) (string, error) {
	name := strings.TrimSpace(s.Unit)
	if name == "" {
		return "", protocol.Errf(protocol.CodeBadRequest,
			"server %q uses the systemd driver but has no \"unit\"", s.ID)
	}
	if !unitPattern.MatchString(name) {
		return "", protocol.Errf(protocol.CodeBadRequest,
			"server %q has a unit name that is not a plausible unit: %q", s.ID, name)
	}
	// A bare name is a .service, which is what systemctl assumes too; being
	// explicit keeps the journal and status calls agreeing about the same unit.
	if !strings.ContainsRune(name, '.') {
		name += ".service"
	}
	return name, nil
}

func (sd *Systemd) Available(ctx context.Context) error {
	// 🚨 systemd being INSTALLED is not the same as systemd running this host.
	// A container with the package present is the common false positive, and
	// reporting the driver available there means every verb fails later
	// instead of the agent saying plainly that it cannot do this.
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return fmt.Errorf("systemd is not managing this host")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "systemctl", "--version").Run(); err != nil {
		return fmt.Errorf("systemctl not usable: %w", err)
	}
	return nil
}

// show reads properties of a unit. Missing units are not an error here: a unit
// that does not exist is a configuration fact the caller wants to report.
func (sd *Systemd) show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	args := []string{"show", unit}
	for _, p := range props {
		args = append(args, "-p", p)
	}

	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(props))
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[k] = v
		}
	}
	return values, nil
}

func (sd *Systemd) Status(ctx context.Context, s Server) (protocol.Status, error) {
	unit, err := sd.unit(s)
	if err != nil {
		return protocol.Status{}, err
	}

	st := protocol.Status{Server: s.ID, Driver: sd.Name(), State: protocol.StateUnknown}

	p, err := sd.show(ctx, unit,
		"LoadState", "ActiveState", "SubState", "MainPID", "ActiveEnterTimestamp", "Result")
	if err != nil {
		return st, protocol.Errf(protocol.CodeDriverFailed, "systemctl show: %v", err)
	}

	// A unit systemd has never heard of is a typo in the config, not a crash.
	// Say which, or the operator goes looking for the wrong thing.
	if p["LoadState"] == "not-found" {
		st.State = protocol.StateStopped
		st.Detail = "no such unit"
		return st, nil
	}

	if pid, err := strconv.Atoi(p["MainPID"]); err == nil && pid > 0 {
		st.PID = pid
	}

	switch p["ActiveState"] {
	case "active":
		// 🚨 "active" alone is not "serving". A Type=simple unit is active the
		// instant it is forked, and a game server can spend a minute loading
		// its world before it will accept anybody — the Aurethil zone takes
		// about seventy seconds. SubState is what separates them, and the
		// health probes are what actually decide readiness.
		if p["SubState"] == "running" {
			st.State = protocol.StateRunning
		} else {
			st.State = protocol.StateStarting
			st.Detail = p["SubState"]
		}
	case "activating":
		st.State = protocol.StateStarting
		st.Detail = p["SubState"]
	case "deactivating":
		st.State = protocol.StateStopping
	case "failed":
		// 🚨 Crashed, not stopped. An operator who stopped a server and an
		// operator whose server died need different things from this screen,
		// and "stopped" tells the second one nothing is wrong.
		st.State = protocol.StateCrashed
		if r := p["Result"]; r != "" && r != "success" {
			st.Detail = r
		}
	case "inactive":
		st.State = protocol.StateStopped
	}

	if ts := p["ActiveEnterTimestamp"]; ts != "" {
		// systemd's own format, e.g. "Fri 2026-09-19 00:31:14 UTC".
		if when, err := time.Parse("Mon 2006-01-02 15:04:05 MST", ts); err == nil {
			utc := when.UTC()
			st.Since = &utc
		}
	}

	return st, nil
}

func (sd *Systemd) Start(ctx context.Context, s Server) error {
	unit, err := sd.unit(s)
	if err != nil {
		return err
	}

	if out, err := exec.CommandContext(ctx, "systemctl", "start", unit).CombinedOutput(); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed,
			"systemctl start %s: %v: %s", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Stop asks systemd to stop the unit and waits for it to actually be gone.
//
// 🚨 The grace period belongs to the UNIT, not to this call. systemd already
// knows how to ask politely and then force, via TimeoutStopSec, and it is the
// operator who decided how long their world takes to save. Overriding that
// from a panel is how a SIGKILL lands in the middle of a save — so this waits
// for the unit rather than imposing its own deadline, and only reports if the
// wait runs out.
func (sd *Systemd) Stop(ctx context.Context, s Server, grace time.Duration) error {
	unit, err := sd.unit(s)
	if err != nil {
		return err
	}

	// A little beyond the caller's grace, so systemd's own timeout is what
	// fires first and its handling is what the operator configured.
	wait := grace + 15*time.Second
	if grace <= 0 {
		wait = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	out, err := exec.CommandContext(ctx, "systemctl", "stop", unit).CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return protocol.Errf(protocol.CodeTimeout,
				"%s did not stop within %s", unit, wait)
		}
		return protocol.Errf(protocol.CodeDriverFailed,
			"systemctl stop %s: %v: %s", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (sd *Systemd) Stats(ctx context.Context, s Server) (protocol.Stats, error) {
	out := protocol.Stats{Server: s.ID, At: time.Now().UTC()}

	st, err := sd.Status(ctx, s)
	if err != nil {
		return out, err
	}
	if st.State != protocol.StateRunning && st.State != protocol.StateStarting {
		return out, protocol.Errf(protocol.CodeNotRunning, "server %q is not running", s.ID)
	}
	if st.PID == 0 {
		return out, protocol.Errf(protocol.CodeNotRunning, "server %q has no main process", s.ID)
	}

	// Same sampler the process driver uses: cgroup v2 where the unit has its
	// own slice — which under systemd it always does — and /proc otherwise.
	snap, err := sampleProcessTree(st.PID)
	if err != nil {
		return out, protocol.Errf(protocol.CodeDriverFailed, "sampling %d: %v", st.PID, err)
	}

	out.CPUPercent = snap.CPUPercent
	out.MemoryBytes = snap.MemoryBytes
	out.MemoryLimit = snap.MemoryLimit
	out.Processes = snap.Processes
	out.Source = snap.Source
	return out, nil
}

func (sd *Systemd) Tail(ctx context.Context, s Server, history int, follow bool, sink func(protocol.Line)) error {
	unit, err := sd.unit(s)
	if err != nil {
		return err
	}

	if history < 0 {
		history = 0
	}

	// 🚨 -o short-iso so every line carries a real timestamp. With -o cat the
	// forum would show "when the agent happened to read it", which is wrong by
	// however long the console was disconnected.
	args := []string{"-u", unit, "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(history)}
	if follow {
		args = append(args, "-f")
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "journalctl: %v", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "journalctl: %v", err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		sink(parseJournalLine(s.ID, sc.Text()))
	}

	_ = cmd.Wait()
	return nil
}

// parseJournalLine splits journalctl's timestamp and unit prefix off a line, so
// the forum shows what the game said and when it said it.
//
// A short-iso line looks like:
//
//	2026-09-19T00:31:14+0000 host AurethilServer[1484316]: the text
func parseJournalLine(server, raw string) protocol.Line {
	l := protocol.Line{Server: server, At: time.Now().UTC(), Text: raw}

	stamp, rest, ok := strings.Cut(raw, " ")
	if !ok {
		return l
	}
	t, err := time.Parse("2006-01-02T15:04:05-0700", stamp)
	if err != nil {
		return l
	}
	l.At = t.UTC()

	// Drop "host unit[pid]: " — everything up to the first ": " after the
	// timestamp. A game line containing a colon is unaffected, because the
	// prefix always comes first.
	if _, after, ok := strings.Cut(rest, ": "); ok {
		l.Text = after
	} else {
		l.Text = rest
	}
	return l
}

// Send is not possible for a unit.
//
// 🚨 Honest refusal rather than a silent no-op. A systemd service has no
// console the agent can write to — there is no stdin to attach and no docker
// exec equivalent. Pretending otherwise would make every console command
// appear to succeed and do nothing, and in-game verification, which delivers
// its code through the console, would fail in a way nobody could explain.
//
// A game that needs commands under this driver should expose RCON or its own
// admin channel, and be configured with the docker or process driver, or have
// `say` pointed at a client that speaks to it.
func (sd *Systemd) Send(ctx context.Context, s Server, line string) error {
	return protocol.Errf(protocol.CodeNotSupported,
		"the systemd driver has no console to write to; a unit has no stdin")
}

// interface guard
var _ Driver = (*Systemd)(nil)
