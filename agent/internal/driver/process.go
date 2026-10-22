package driver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ernestdefoe/garrison/internal/protocol"
	"github.com/ernestdefoe/garrison/internal/supervise"
)

// Process runs a game server directly: no Docker, no systemd, nothing between
// the agent and the binary.
//
// 🚨 This is the driver that has to be good, not the fallback. A folder from
// SteamCMD with a start script is how most game servers on earth actually run,
// and it is the case every panel handles worst. Everything Docker gives away
// free — a stdout stream, a grace period on stop, a restart policy, resource
// accounting — this driver has to provide itself.
type Process struct {
	mu   sync.Mutex
	proc map[string]*supervise.Process // by server ID
}

// NewProcess builds the process driver.
func NewProcess() *Process {
	return &Process{proc: make(map[string]*supervise.Process)}
}

func (p *Process) Name() string { return "process" }

// Available is always true: running a program is the one thing every host can
// do. It is the reason this driver is the floor of the product.
func (p *Process) Available(ctx context.Context) error { return nil }

func (p *Process) get(id string) (*supervise.Process, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.proc[id]
	return pr, ok
}

func (p *Process) Status(ctx context.Context, s Server) (protocol.Status, error) {
	st := protocol.Status{Server: s.ID, Driver: p.Name(), State: protocol.StateStopped}

	pr, ok := p.get(s.ID)
	if !ok {
		// Nothing running under this agent. It may still be running under
		// somebody else — adopting a process the agent did not start is a
		// later capability, and saying "stopped" here would be a lie we
		// cannot yet avoid, so say so plainly in Detail.
		st.Detail = "not started by this agent"
		return st, nil
	}

	snap := pr.Snapshot()
	st.PID = snap.PID
	if !snap.Started.IsZero() {
		st.Since = &snap.Started
	}
	switch {
	case snap.Running:
		st.State = protocol.StateRunning
	case snap.ExitCode != 0:
		st.State = protocol.StateCrashed
		st.Detail = fmt.Sprintf("exited with status %d", snap.ExitCode)
	default:
		st.State = protocol.StateStopped
	}
	return st, nil
}

func (p *Process) Start(ctx context.Context, s Server) error {
	if s.Command == "" {
		return protocol.Errf(protocol.CodeBadRequest, "server %q has no command configured", s.ID)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if pr, ok := p.proc[s.ID]; ok && pr.Snapshot().Running {
		return protocol.Errf(protocol.CodeAlreadyRunning, "server %q is already running", s.ID)
	}

	dir := s.Dir
	if dir == "" {
		dir = filepath.Dir(s.Command)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return protocol.Errf(protocol.CodeBadRequest, "working directory %q is not usable", dir)
	}

	pr, err := supervise.Start(supervise.Config{
		Dir:     dir,
		Command: s.Command,
		Args:    s.Args,
		Env:     s.Env,
	})
	if err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "starting %q: %v", s.ID, err)
	}
	p.proc[s.ID] = pr
	return nil
}

// Stop asks, waits, and only then forces.
//
// 🚨 The order matters and the waiting matters. Games write their world on
// shutdown; a SIGKILL during that write is a corrupt save, and the operator
// will blame the panel that sent it — correctly.
func (p *Process) Stop(ctx context.Context, s Server, grace time.Duration) error {
	pr, ok := p.get(s.ID)
	if !ok || !pr.Snapshot().Running {
		return protocol.Errf(protocol.CodeNotRunning, "server %q is not running", s.ID)
	}

	// 1. The politest thing available: ask the game itself, in its own
	// language, if the operator told us how.
	if s.StopCommand != "" {
		if err := pr.Send(s.StopCommand); err == nil {
			if pr.WaitFor(ctx, grace) {
				return nil
			}
		}
	}

	// 2. SIGTERM, which every well-behaved server treats as "save and exit".
	if err := pr.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return protocol.Errf(protocol.CodeDriverFailed, "signalling %q: %v", s.ID, err)
	}
	if pr.WaitFor(ctx, grace) {
		return nil
	}

	// 3. Only now. Reaching here is worth surfacing: a server that ignores
	// SIGTERM for its whole grace period is misconfigured, not merely slow.
	if err := pr.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return protocol.Errf(protocol.CodeDriverFailed, "killing %q: %v", s.ID, err)
	}
	if !pr.WaitFor(ctx, 5*time.Second) {
		return protocol.Errf(protocol.CodeTimeout, "server %q survived SIGKILL", s.ID)
	}
	return nil
}

func (p *Process) Stats(ctx context.Context, s Server) (protocol.Stats, error) {
	out := protocol.Stats{Server: s.ID, At: time.Now().UTC()}

	pr, ok := p.get(s.ID)
	if !ok {
		return out, protocol.Errf(protocol.CodeNotRunning, "server %q is not running", s.ID)
	}
	snap := pr.Snapshot()
	if !snap.Running {
		return out, protocol.Errf(protocol.CodeNotRunning, "server %q is not running", s.ID)
	}

	// 🚨 There is no `docker stats` here. sampleProcessTree is per-platform:
	// cgroup v2 where the process has its own slice, /proc walked over the
	// whole tree where it does not, and ps on anything else. A Minecraft
	// server's real memory is the JVM plus whatever it forked, so sampling
	// the top-level PID alone reports a number that is always too small.
	sample, err := sampleProcessTree(snap.PID)
	if err != nil {
		return out, protocol.Errf(protocol.CodeDriverFailed, "sampling %q: %v", s.ID, err)
	}
	out.CPUPercent = sample.CPUPercent
	out.MemoryBytes = sample.MemoryBytes
	out.MemoryLimit = sample.MemoryLimit
	out.Processes = sample.Processes
	out.Source = sample.Source
	return out, nil
}

func (p *Process) Tail(ctx context.Context, s Server, history int, follow bool, sink func(protocol.Line)) error {
	pr, ok := p.get(s.ID)
	if !ok {
		return protocol.Errf(protocol.CodeNotRunning, "server %q has not been started by this agent", s.ID)
	}
	return pr.Tail(ctx, history, follow, func(l supervise.Line) {
		sink(protocol.Line{Server: s.ID, At: l.At, Text: l.Text, Stderr: l.Stderr})
	})
}

func (p *Process) Send(ctx context.Context, s Server, line string) error {
	pr, ok := p.get(s.ID)
	if !ok || !pr.Snapshot().Running {
		return protocol.Errf(protocol.CodeNotRunning, "server %q is not running", s.ID)
	}
	if err := pr.Send(line); err != nil {
		return protocol.Errf(protocol.CodeDriverFailed, "writing to %q: %v", s.ID, err)
	}
	return nil
}

// Shutdown stops supervising, without stopping the games. Used when the agent
// itself is going down: an agent restart must not take the servers with it.
func (p *Process) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pr := range p.proc {
		pr.Detach()
	}
}

// interface guard
var _ Driver = (*Process)(nil)

// unused imports guard for platforms where sampling does not need them
var (
	_ = bufio.NewReader
	_ = io.Discard
	_ = exec.Command
)
