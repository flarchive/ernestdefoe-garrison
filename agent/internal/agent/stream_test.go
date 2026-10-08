package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/protocol"
)

// followDriver blocks in a following Tail until its context ends, the way
// `journalctl -f` / `docker logs -f` does, and counts how many are open.
type followDriver struct {
	fakeDriver
	open    atomic.Int32
	history atomic.Int32
}

func (f *followDriver) Tail(ctx context.Context, _ driver.Server, history int, follow bool, _ func(protocol.Line)) error {
	f.history.Store(int32(history))
	if !follow {
		return nil
	}
	f.open.Add(1)
	defer f.open.Add(-1)
	<-ctx.Done()
	return nil
}

func newFollowAgent(t *testing.T) (*Agent, *followDriver) {
	t.Helper()
	fd := &followDriver{fakeDriver: fakeDriver{name: "fake"}}
	a, _ := New(context.Background(),
		[]driver.Server{{ID: "valheim", Name: "Shattered Pact", Driver: "fake", StopGraceSeconds: 45}},
		driver.Set{"fake": fd})
	t.Cleanup(a.Shutdown)
	return a, fd
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 🚨 Nothing on the forum cancels a follow, so every one a caller opens has to
// be bounded by the agent itself — in number, or a repeated request piles up
// log followers on the game host for ever.
func TestFollowingTailsAreCappedInNumber(t *testing.T) {
	a, fd := newFollowAgent(t)

	for i := 0; i < MaxStreams; i++ {
		res := handle(t, a, protocol.Request{ID: fmt.Sprintf("s%d", i), Verb: protocol.VerbConsoleTail, Server: "valheim",
			Params: []byte(`{"follow":true}`)})
		if !res.OK {
			t.Fatalf("stream %d refused below the cap: %v", i, res.Error)
		}
	}
	waitFor(t, "the streams to open", func() bool { return fd.open.Load() == MaxStreams })

	res := handle(t, a, protocol.Request{ID: "one-too-many", Verb: protocol.VerbConsoleTail, Server: "valheim",
		Params: []byte(`{"follow":true}`)})
	if res.OK {
		t.Fatal("a follow beyond the cap was accepted")
	}

	// Re-using an open stream's ID replaces it rather than counting twice.
	res = handle(t, a, protocol.Request{ID: "s0", Verb: protocol.VerbConsoleTail, Server: "valheim",
		Params: []byte(`{"follow":true}`)})
	if !res.OK {
		t.Fatalf("replacing a stream by its own ID was refused: %v", res.Error)
	}
	time.Sleep(50 * time.Millisecond)
	if got := fd.open.Load(); got != MaxStreams {
		t.Fatalf("%d followers open after a replacement, want %d", got, MaxStreams)
	}
}

// ...and in time: a follow nobody cancels ends by itself.
func TestAFollowingTailEndsByItself(t *testing.T) {
	was := StreamLifetime
	StreamLifetime = 50 * time.Millisecond
	t.Cleanup(func() { StreamLifetime = was })

	a, fd := newFollowAgent(t)

	res := handle(t, a, protocol.Request{ID: "forgotten", Verb: protocol.VerbConsoleTail, Server: "valheim",
		Params: []byte(`{"follow":true}`)})
	if !res.OK {
		t.Fatalf("follow refused: %v", res.Error)
	}

	waitFor(t, "the uncancelled follow to end", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return fd.open.Load() == 0 && len(a.streams) == 0
	})
}

func TestTailHistoryIsCapped(t *testing.T) {
	a, fd := newFollowAgent(t)

	res := handle(t, a, protocol.Request{ID: "h", Verb: protocol.VerbConsoleTail, Server: "valheim",
		Params: []byte(`{"history":100000000}`)})
	if !res.OK {
		t.Fatalf("tail refused: %v", res.Error)
	}
	if got := fd.history.Load(); got != MaxTailHistory {
		t.Fatalf("driver asked for %d lines of history, want the cap %d", got, MaxTailHistory)
	}
}
