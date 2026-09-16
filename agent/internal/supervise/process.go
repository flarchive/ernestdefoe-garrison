// Package supervise runs and watches a child process.
//
// It exists because the process driver has to provide, by hand, everything a
// container runtime gives away: a stdout stream somebody can subscribe to
// after the fact, a writable stdin for console commands, an exit status, and
// a process group that can be signalled as a unit.
package supervise

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Line is one line of output from the child.
type Line struct {
	At     time.Time
	Text   string
	Stderr bool
}

// Config describes one process to run.
type Config struct {
	Dir     string
	Command string
	Args    []string
	Env     map[string]string

	// Scrollback is how many lines to retain for late subscribers. Zero uses
	// DefaultScrollback.
	Scrollback int
}

// DefaultScrollback is deliberately generous: the first thing anybody does
// with a crashed server is open the console and scroll up, and a buffer that
// starts at the moment they clicked is useless for exactly that.
const DefaultScrollback = 2000

// Snapshot is a consistent read of a process's state.
type Snapshot struct {
	PID      int
	Running  bool
	Started  time.Time
	Exited   time.Time
	ExitCode int
}

// Process is a running (or finished) child.
type Process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu        sync.RWMutex
	running   bool
	started   time.Time
	exited    time.Time
	exitCode  int
	detached  bool
	ring      []Line
	ringCap   int
	ringStart int // index of the oldest line when the ring has wrapped
	ringLen   int

	subsMu sync.Mutex
	subs   map[int]chan Line
	nextID int

	done chan struct{}
}

// Start launches the process and begins capturing its output.
func Start(cfg Config) (*Process, error) {
	if cfg.Scrollback <= 0 {
		cfg.Scrollback = DefaultScrollback
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir

	env := os.Environ()
	for k, v := range cfg.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// 🚨 Its own process group. A game server forks — a wrapper script, a JVM,
	// a crash handler — and signalling only the PID we launched leaves the
	// children running and the port held, so the next start fails with
	// "address already in use" and looks like a Garrison bug.
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	p := &Process{
		cmd:     cmd,
		stdin:   stdin,
		running: true,
		started: time.Now().UTC(),
		ring:    make([]Line, cfg.Scrollback),
		ringCap: cfg.Scrollback,
		subs:    make(map[int]chan Line),
		done:    make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); p.consume(stdout, false) }()
	go func() { defer wg.Done(); p.consume(stderr, true) }()

	go func() {
		wg.Wait() // drain both pipes before reaping, or we lose the last words
		err := cmd.Wait()

		p.mu.Lock()
		p.running = false
		p.exited = time.Now().UTC()
		if ee, ok := err.(*exec.ExitError); ok {
			p.exitCode = ee.ExitCode()
		} else if err != nil {
			p.exitCode = -1
		}
		p.mu.Unlock()

		close(p.done)
		p.closeSubs()
	}()

	return p, nil
}

func (p *Process) consume(r io.Reader, stderr bool) {
	sc := bufio.NewScanner(r)
	// Game servers print long lines — a Java stack trace, a mod list. The
	// default 64K token limit turns those into a scanner error that silently
	// ends the console.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p.emit(Line{At: time.Now().UTC(), Text: sc.Text(), Stderr: stderr})
	}
}

func (p *Process) emit(l Line) {
	p.mu.Lock()
	idx := (p.ringStart + p.ringLen) % p.ringCap
	p.ring[idx] = l
	if p.ringLen < p.ringCap {
		p.ringLen++
	} else {
		p.ringStart = (p.ringStart + 1) % p.ringCap
	}
	p.mu.Unlock()

	p.subsMu.Lock()
	for _, ch := range p.subs {
		select {
		case ch <- l:
		default:
			// 🚨 Drop rather than block. One slow console viewer must never
			// stall the process's own stdout — that back-pressures the game
			// itself and eventually hangs it.
		}
	}
	p.subsMu.Unlock()
}

// Snapshot reads the process state.
func (p *Process) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	return Snapshot{
		PID:      pid,
		Running:  p.running,
		Started:  p.started,
		Exited:   p.exited,
		ExitCode: p.exitCode,
	}
}

// History returns the last n retained lines, oldest first.
func (p *Process) History(n int) []Line {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if n <= 0 || n > p.ringLen {
		n = p.ringLen
	}
	out := make([]Line, 0, n)
	for i := p.ringLen - n; i < p.ringLen; i++ {
		out = append(out, p.ring[(p.ringStart+i)%p.ringCap])
	}
	return out
}

// Tail delivers history then, if follow, live lines until ctx ends.
func (p *Process) Tail(ctx context.Context, history int, follow bool, sink func(Line)) error {
	if !follow {
		for _, l := range p.History(history) {
			sink(l)
		}
		return nil
	}

	// Subscribe BEFORE replaying history, so a line printed between the two
	// is duplicated rather than lost. A repeated line is a cosmetic annoyance; a
	// missing one is somebody debugging a crash they cannot see.
	ch := make(chan Line, 256)
	p.subsMu.Lock()
	id := p.nextID
	p.nextID++
	p.subs[id] = ch
	p.subsMu.Unlock()

	defer func() {
		p.subsMu.Lock()
		if c, ok := p.subs[id]; ok {
			delete(p.subs, id)
			close(c)
		}
		p.subsMu.Unlock()
	}()

	for _, l := range p.History(history) {
		sink(l)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case l, ok := <-ch:
			if !ok {
				return nil // process exited
			}
			sink(l)
		}
	}
}

func (p *Process) closeSubs() {
	p.subsMu.Lock()
	defer p.subsMu.Unlock()
	for id, ch := range p.subs {
		delete(p.subs, id)
		close(ch)
	}
}

// Send writes one line to the child's stdin.
func (p *Process) Send(line string) error {
	p.mu.RLock()
	running := p.running
	p.mu.RUnlock()
	if !running {
		return os.ErrProcessDone
	}
	_, err := io.WriteString(p.stdin, line+"\n")
	return err
}

// Signal sends sig to the whole process group.
func (p *Process) Signal(sig syscall.Signal) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd.Process == nil {
		return os.ErrProcessDone
	}
	return signalGroup(p.cmd.Process.Pid, sig)
}

// WaitFor blocks until the process exits or d elapses. It reports whether the
// process actually exited.
func (p *Process) WaitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.done:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// Detach stops supervising without signalling the child, so that restarting
// the agent does not take the game servers down with it.
func (p *Process) Detach() {
	p.mu.Lock()
	p.detached = true
	p.mu.Unlock()
	_ = p.stdin.Close()
}
