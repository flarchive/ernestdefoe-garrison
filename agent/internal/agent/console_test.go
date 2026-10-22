package agent

import (
	"context"
	"testing"
	"time"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/protocol"
)

// logDriver replays a fixed script of console lines.
type logDriver struct {
	fakeDriver
	lines []string
}

func (d *logDriver) Tail(_ context.Context, s driver.Server, _ int, _ bool, sink func(protocol.Line)) error {
	for _, t := range d.lines {
		sink(protocol.Line{Server: s.ID, At: time.Now(), Text: t})
	}
	return nil
}

func shipperFixture(lines ...string) (*consoleShipper, *Agent, *logDriver) {
	d := &logDriver{fakeDriver: fakeDriver{name: "log"}, lines: lines}
	a, _ := New(context.Background(),
		[]driver.Server{{ID: "srv", Name: "Server", Driver: "log"}},
		driver.Set{"log": d})

	return newConsoleShipper(), a, d
}

func texts(ls []protocol.Line) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Text)
	}
	return out
}

func TestFirstCollectShipsEverythingItCanSee(t *testing.T) {
	c, a, _ := shipperFixture("one", "two", "three")

	got := texts(c.collect(context.Background(), a))
	if len(got) != 3 {
		t.Fatalf("got %v, want all three lines on the first poll", got)
	}
}

func TestSecondCollectShipsOnlyWhatIsNew(t *testing.T) {
	c, a, d := shipperFixture("one", "two", "three")
	c.collect(context.Background(), a)

	d.lines = append(d.lines, "four", "five")

	got := texts(c.collect(context.Background(), a))
	if len(got) != 2 || got[0] != "four" || got[1] != "five" {
		t.Fatalf("got %v, want [four five]", got)
	}
}

func TestNothingNewShipsNothing(t *testing.T) {
	c, a, _ := shipperFixture("one", "two")
	c.collect(context.Background(), a)

	if got := texts(c.collect(context.Background(), a)); len(got) != 0 {
		t.Fatalf("got %v, want nothing on an unchanged log", got)
	}
}

// 🚨 THE ONE THAT MATTERS.
//
// Game servers repeat themselves constantly — "Connections 0" every ten
// seconds, the same autosave line, an identical warning every minute. If the
// shipper looks FORWARDS for the line it last sent, it finds the earliest
// occurrence, which on a repetitive log is far in the past, and re-ships
// everything after it. The console then fills with duplicates of output the
// operator has already read, which is how a console becomes unusable exactly
// when somebody needs it.
func TestARepeatedLineDoesNotResendTheWholeLog(t *testing.T) {
	heartbeat := "Connections 0 ZDOS:123229  sent:0 recv:0"

	c, a, d := shipperFixture(
		heartbeat,
		"World save (5/5) done",
		heartbeat,
		"Player joined",
		heartbeat,
	)

	first := texts(c.collect(context.Background(), a))
	if len(first) != 5 {
		t.Fatalf("first poll got %v, want all five", first)
	}

	// The next poll sees the same log with one more heartbeat on the end.
	d.lines = append(d.lines, heartbeat)

	got := texts(c.collect(context.Background(), a))

	if len(got) != 1 {
		t.Fatalf("got %d lines %v — a repeated line made the shipper resend history", len(got), got)
	}
	if got[0] != heartbeat {
		t.Fatalf("got %q, want the new heartbeat", got[0])
	}
}

// 🚨 One noisy server must not be able to fill the poll body and take every
// other server's status down with it.
func TestOneServerCannotFloodAPoll(t *testing.T) {
	lines := make([]string, 0, MaxLinesPerServer*3)
	for i := 0; i < MaxLinesPerServer*3; i++ {
		lines = append(lines, "line "+time.Duration(i).String())
	}

	c, a, _ := shipperFixture(lines...)

	got := c.collect(context.Background(), a)
	if len(got) > MaxLinesPerServer {
		t.Fatalf("shipped %d lines, cap is %d", len(got), MaxLinesPerServer)
	}
}

// After a restart the interesting output is the crash that preceded it — and
// that is exactly what the shipper would otherwise skip as already seen.
func TestForgetCausesRecentOutputToBeResent(t *testing.T) {
	c, a, _ := shipperFixture("one", "two", "three")
	c.collect(context.Background(), a)

	c.forget("srv")

	if got := texts(c.collect(context.Background(), a)); len(got) != 3 {
		t.Fatalf("got %v, want the window re-sent after forget", got)
	}
}
