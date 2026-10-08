package supervise

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestCapturesStdoutAndStderrIntoScrollback(t *testing.T) {
	p, err := Start(Config{
		Command: script(t, "talk.sh", "echo hello; echo trouble >&2; sleep 30"),
		Dir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Signal(syscall.SIGKILL)

	if !waitFor(t, 3*time.Second, func() bool { return len(p.History(0)) >= 2 }) {
		t.Fatalf("captured %d lines, want 2", len(p.History(0)))
	}

	var out, errLine bool
	for _, l := range p.History(0) {
		if l.Text == "hello" && !l.Stderr {
			out = true
		}
		if l.Text == "trouble" && l.Stderr {
			errLine = true
		}
	}
	if !out {
		t.Error("stdout line missing")
	}
	// 🚨 stderr is not an error. A Java game server logs there by default, so
	// a console that drops it shows an empty screen for a running server.
	if !errLine {
		t.Error("stderr line missing, or not marked as stderr")
	}
}

func TestScrollbackKeepsTheMostRecentLinesWhenItWraps(t *testing.T) {
	p, err := Start(Config{
		Command:    script(t, "count.sh", "i=0; while [ $i -lt 50 ]; do echo line$i; i=$((i+1)); done; sleep 30"),
		Dir:        t.TempDir(),
		Scrollback: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Signal(syscall.SIGKILL)

	if !waitFor(t, 3*time.Second, func() bool {
		h := p.History(0)
		return len(h) == 10 && h[len(h)-1].Text == "line49"
	}) {
		h := p.History(0)
		t.Fatalf("history is %d lines ending %q; want 10 ending line49", len(h), last(h))
	}

	h := p.History(0)
	if h[0].Text != "line40" {
		t.Errorf("oldest retained line is %q, want line40 — the ring dropped the wrong end", h[0].Text)
	}
}

func last(ls []Line) string {
	if len(ls) == 0 {
		return ""
	}
	return ls[len(ls)-1].Text
}

func TestSendReachesTheProcessStdin(t *testing.T) {
	p, err := Start(Config{
		Command: script(t, "echo.sh", "while read line; do echo \"got:$line\"; done"),
		Dir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Signal(syscall.SIGKILL)

	if err := p.Send("save-all"); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		for _, l := range p.History(0) {
			if l.Text == "got:save-all" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("the line never came back; stdin is not wired to the console")
	}
}

// 🚨 The stop ladder, proved on a process that COOPERATES: SIGTERM is enough,
// and the caller does not wait out the whole grace period for a server that
// shut down in a second.
func TestGracefulStopDoesNotWaitOutTheGracePeriod(t *testing.T) {
	p, err := Start(Config{
		Command: script(t, "polite.sh", `
trap 'echo saving; exit 0' TERM
while true; do sleep 0.05; done`),
		Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Let the trap install before signalling, or the shell dies on the
	// default disposition and the test proves nothing.
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Running })
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if !p.WaitFor(context.Background(), 10*time.Second) {
		t.Fatal("process ignored SIGTERM")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %s to honour SIGTERM; a cooperative stop should be prompt", elapsed)
	}
	if p.Snapshot().Running {
		t.Fatal("snapshot still says running after exit")
	}
}

// 🚨 And on a process that REFUSES. This is the half that matters: a server
// which ignores SIGTERM must still be stopped, but only after the grace period
// has genuinely elapsed. A ladder that skips to SIGKILL early is how a world
// file gets truncated mid-save.
func TestStubbornProcessIsKilledButOnlyAfterTheGrace(t *testing.T) {
	p, err := Start(Config{
		Command: script(t, "stubborn.sh", `
trap '' TERM
while true; do sleep 0.05; done`),
		Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return p.Snapshot().Running })
	time.Sleep(200 * time.Millisecond)

	const grace = 700 * time.Millisecond
	start := time.Now()

	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if p.WaitFor(context.Background(), grace) {
		t.Fatal("process claimed to exit on a SIGTERM it was ignoring")
	}
	if err := p.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !p.WaitFor(context.Background(), 5*time.Second) {
		t.Fatal("survived SIGKILL")
	}

	if elapsed := time.Since(start); elapsed < grace {
		t.Fatalf("killed after %s, before the %s grace elapsed", elapsed, grace)
	}
}

// 🚨 The forked-child case, which is the whole reason for the process group.
//
// A game server forks: a wrapper script launches a JVM, SteamCMD launches the
// binary, a crash handler sits in between. Signalling only the PID we launched
// leaves the grandchild alive holding the game's UDP port, so the next start
// fails with "address already in use" — which reads as a Garrison bug and is
// not one.
func TestSignalReachesForkedGrandchildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")

	p, err := Start(Config{
		Command: script(t, "wrapper.sh", `
# Stand in for a launcher script that execs the real server as a child.
sh -c 'while true; do sleep 0.05; done' &
echo $! > `+pidFile+`
echo started
wait`),
		Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	var grandchild int
	if !waitFor(t, 5*time.Second, func() bool {
		b, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		grandchild, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil && grandchild > 0
	}) {
		t.Fatal("the wrapper never reported a grandchild pid")
	}

	if !alive(grandchild) {
		t.Fatal("precondition: the grandchild should be running")
	}

	if err := p.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	p.WaitFor(context.Background(), 5*time.Second)

	if !waitFor(t, 5*time.Second, func() bool { return !alive(grandchild) }) {
		// Clean up before failing, or the test leaves a process behind.
		_ = syscall.Kill(grandchild, syscall.SIGKILL)
		t.Fatalf("grandchild %d survived; the signal did not reach the process group "+
			"and a real server would still be holding its port", grandchild)
	}
}

// alive reports whether pid exists. Signal 0 performs the permission and
// existence checks without delivering anything.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestExitCodeIsRecorded(t *testing.T) {
	p, err := Start(Config{Command: script(t, "fail.sh", "exit 3"), Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !p.WaitFor(context.Background(), 5*time.Second) {
		t.Fatal("never exited")
	}
	snap := p.Snapshot()
	if snap.Running {
		t.Fatal("still marked running")
	}
	// 🚨 The difference between "stopped" and "crashed" in the UI is this
	// number, and an operator seeing "stopped" for a server that died is the
	// outage they find out about hours later.
	if snap.ExitCode != 3 {
		t.Fatalf("exit code %d, want 3", snap.ExitCode)
	}
}

func TestTailFollowsLiveLinesAndStopsWithTheContext(t *testing.T) {
	p, err := Start(Config{
		Command: script(t, "drip.sh", "echo first; sleep 0.2; echo second; sleep 30"),
		Dir:     t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Signal(syscall.SIGKILL)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	seen := make(chan string, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Tail(ctx, 100, true, func(l Line) {
			select {
			case seen <- l.Text:
			default:
			}
		})
	}()

	got := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case s := <-seen:
			got[s] = true
		case <-deadline:
			t.Fatalf("only saw %v; a follow must deliver lines printed after it subscribed", got)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Tail did not return when its context was cancelled — every console tab would leak one")
	}
}
