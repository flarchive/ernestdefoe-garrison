package agent

import (
	"context"
	"sync"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/protocol"
)

/*
🚨 The console ships CONTINUOUSLY, not on subscription.

The tempting design is a stream somebody opens: the forum queues a follow when
a console tab is opened and cancels it when the tab closes. It is more
efficient and it is wrong for this product, for two reasons.

First, the whole point of retained console output is the person who arrives
AFTER something went wrong. A stream that starts when you open the tab shows
you nothing about the crash you came to investigate — which is exactly the
moment somebody needs it.

Second, subscribe/unsubscribe is bookkeeping across an unreliable link. A tab
closed by a browser crash, a laptop lid, a dropped connection: every one of
those leaks a follower on the agent, and an agent accumulating one `docker
logs --follow` per tab anybody ever opened is a slow leak nobody notices until
a host runs out of file handles.

Shipping a bounded tail on every poll costs a little bandwidth and has neither
failure mode.
*/

// consoleShipper remembers what it has already sent for each server, so a poll
// carries only what is new.
type consoleShipper struct {
	mu sync.Mutex

	// The last few lines shipped per server, used as a fingerprint to find
	// where the previous window ended inside the new one.
	seen map[string][]string
}

// FingerprintLines is how many trailing lines identify "where I got to".
//
// 🚨 A BLOCK, not a single line, and this is the whole subtlety of the file.
//
// Matching on one remembered line looks obviously correct and is wrong on
// every real game server, because game servers repeat themselves: "Connections
// 0" every ten seconds, the same autosave line, an identical warning every
// minute. Search forwards for that line and you find its earliest occurrence,
// far in the past, and re-ship everything after it. Search backwards and you
// find the occurrence that just arrived, and ship nothing at all — the console
// silently stops updating, which is worse.
//
// A run of consecutive lines is not fooled by either. Twenty is comfortably
// more than any heartbeat cycle and still cheap to hold.
const FingerprintLines = 20

// MaxLinesPerServer bounds one poll's worth of output for one server.
//
// 🚨 Bounded, because a busy Minecraft server with a chatty mod can produce
// thousands of lines a minute. Unbounded, a single misbehaving server would
// fill the poll body, time out the request, and take every OTHER server's
// status down with it — one noisy game silencing the whole host.
const MaxLinesPerServer = 200

func newConsoleShipper() *consoleShipper {
	return &consoleShipper{seen: make(map[string][]string)}
}

// collect returns the console lines that are new since the last poll.
func (c *consoleShipper) collect(ctx context.Context, a *Agent) []protocol.Line {
	var out []protocol.Line

	for _, s := range a.servers {
		drv, ok := a.drivers[s.Driver]
		if !ok {
			continue
		}

		lines := c.newFor(ctx, a, drv, s)
		out = append(out, lines...)
	}

	return out
}

func (c *consoleShipper) newFor(ctx context.Context, a *Agent, drv driver.Driver, s driver.Server) []protocol.Line {
	var all []protocol.Line

	// Read a window rather than everything: the agent only needs to work out
	// which lines are new, and a server that produced more than this since the
	// last poll has its older lines dropped on purpose — see MaxLinesPerServer.
	err := drv.Tail(ctx, s, MaxLinesPerServer*2, false, func(l protocol.Line) {
		all = append(all, l)
	})

	if err != nil || len(all) == 0 {
		return nil
	}

	c.mu.Lock()
	previous := c.seen[s.ID]
	c.mu.Unlock()

	fresh := all

	if len(previous) > 0 {
		// Where does the previous window's tail end inside this one?
		if cut := findBlock(all, previous); cut >= 0 {
			fresh = all[cut:]
		}
		// Not found means the log rotated or the server restarted, and
		// everything visible is genuinely new.
	}

	/*
	 * 🚨 The player watcher sees these lines HERE, before the cap below.
	 *
	 * The same read, used twice: the console panel gets a bounded window and
	 * the watcher gets every new line. Reading the log a second time for
	 * players would double the I/O on a busy server for output already in
	 * memory — and, worse, the two reads could disagree, so the console would
	 * show a join the player list did not have.
	 *
	 * Before the cap, because a server that produced more than MaxLinesPerServer
	 * since the last poll drops its OLDEST console lines on purpose — and those
	 * are exactly the ones most likely to carry a join that the player set must
	 * not miss.
	 */
	if w := a.watcher(s.ID); w != nil {
		for _, l := range fresh {
			w.Observe(l.Text)
		}
	}

	if len(fresh) == 0 {
		return nil
	}

	// Newest wins if the server outran the window.
	if len(fresh) > MaxLinesPerServer {
		fresh = fresh[len(fresh)-MaxLinesPerServer:]
	}

	c.mu.Lock()
	c.seen[s.ID] = fingerprint(all)
	c.mu.Unlock()

	return fresh
}

// fingerprint takes the last FingerprintLines texts of a window.
func fingerprint(lines []protocol.Line) []string {
	start := len(lines) - FingerprintLines
	if start < 0 {
		start = 0
	}

	out := make([]string, 0, len(lines)-start)
	for _, l := range lines[start:] {
		out = append(out, l.Text)
	}

	return out
}

// findBlock returns the index just past the LAST occurrence of block in lines,
// or -1. Searched from the end because the most recent match is the one that
// corresponds to where this agent actually got to.
func findBlock(lines []protocol.Line, block []string) int {
	if len(block) == 0 || len(block) > len(lines) {
		return -1
	}

	for start := len(lines) - len(block); start >= 0; start-- {
		match := true

		for i, want := range block {
			if lines[start+i].Text != want {
				match = false
				break
			}
		}

		if match {
			return start + len(block)
		}
	}

	return -1
}

// forget drops a server's position, so the next poll re-sends its recent
// output. Used after a restart: the interesting lines are the ones explaining
// why it went down, and the shipper would otherwise skip them as "seen".
func (c *consoleShipper) forget(serverID string) {
	c.mu.Lock()
	delete(c.seen, serverID)
	c.mu.Unlock()
}
