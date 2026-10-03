// Package agent dispatches verbs to drivers and holds the link to the forum.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/ernestdefoe/garrison/internal/backup"
	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/health"
	"github.com/ernestdefoe/garrison/internal/offsite"
	"github.com/ernestdefoe/garrison/internal/players"
	"github.com/ernestdefoe/garrison/internal/protocol"
	"github.com/ernestdefoe/garrison/internal/provision"
	"github.com/ernestdefoe/garrison/internal/settings"
)

// Version is the agent build. The forum shows it, and an agent several
// versions behind is itself an incident worth surfacing.
const Version = "0.1.0-spike"

// Agent owns the servers, the drivers and the in-flight streams.
type Agent struct {
	servers map[string]driver.Server
	drivers driver.Set

	mu      sync.Mutex
	streams map[string]context.CancelFunc // by request ID

	// The last off-site copy per server, so status can report it without
	// asking the bucket on every poll. See OffsiteStatus for why.
	offsiteMu   sync.Mutex
	offsiteLast map[string]offsiteOutcome

	// Who is in each game, read from its log. Nil for a server whose operator
	// did not configure how to read them.
	watchers map[string]*players.Watcher

	/*
	 * What may be installed, and how to persist a server once it is.
	 *
	 * 🚨 `register` is nil unless the agent was built from a config file. A
	 * provisioned server that cannot be written down is one that vanishes on
	 * the next restart, so the agent says so rather than pretending.
	 */
	templates []provision.Template
	register  func(driver.Server) error

	// Installs in flight, so a second click cannot start a second steamcmd
	// writing the same directory.
	installing map[string]struct{}

	// The agent's own lifetime, for work that outlives the request that began
	// it. See runInstall.
	ctx context.Context

	/*
	 * The work the agent started that outlives the request that began it: an
	 * install running in the background, a console being followed.
	 *
	 * 🚨 COUNTED, so shutdown can wait for it rather than walk away from it.
	 *
	 * runInstall creates the install directory, writes the operator's config
	 * file and then the agent's own maps. A process that stops in the middle
	 * of that leaves a config which is neither the old one nor the new one,
	 * and a directory half full of a game nobody asked to keep. The goroutine
	 * has to outlive its request; it must not outlive the agent.
	 *
	 * The tests found this before a host did. An install goroutine still
	 * creating its directory after the test that started it had returned
	 * raced t.TempDir's removal of that directory, and the test failed in
	 * cleanup — one run in eight, with an error that named no code of ours.
	 */
	background sync.WaitGroup

	/*
	 * Which DRIVERS this host cannot run, kept so `--check` can report them.
	 *
	 * 🚨 Remembered rather than only returned from New(): the preflight report
	 * runs after construction, and a missing Docker is exactly what an operator
	 * running --check needs told. Returning it once and discarding it meant the
	 * one caller who most needed it could not reach it.
	 *
	 * Drivers only. Per-server features — a bad player pattern, an unreadable
	 * config file — are reported by the per-server check, which has room to say
	 * which server and what to do about it.
	 */
	unavailable []string
}

// Unavailable lists the drivers this host cannot run.
func (a *Agent) Unavailable() []string {
	return a.unavailable
}

/*
watcher returns a server's player watcher, or nil.

🚨 Built ONCE at startup rather than per poll, because the watcher IS the state:
it holds the current set, and rebuilding it every poll would empty it every
fifteen seconds and report an empty game.
*/
func (a *Agent) watcher(serverID string) *players.Watcher {
	/*
	 * 🚨 Locked, because a finished install adds to this map from its own
	 * goroutine while a poll is reading it. An unguarded map read against a
	 * concurrent write is not a stale answer, it is a fatal runtime error
	 * that takes the whole agent — and every server it was watching — down.
	 */
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.watchers[serverID]
}

// server looks one server up by id.
func (a *Agent) server(id string) (driver.Server, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.servers[id]

	return s, ok
}

// serverList snapshots the servers this agent knows about.
//
// 🚨 A copy, taken under the lock, rather than the map itself. Callers iterate
// it while an install may be registering a new server, and ranging over the
// live map from outside the lock is the same fatal error as reading it.
func (a *Agent) serverList() []driver.Server {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]driver.Server, 0, len(a.servers))
	for _, s := range a.servers {
		out = append(out, s)
	}

	return out
}

// New builds an agent over the servers it is configured with, keeping only
// the drivers that actually work on this host.
func New(ctx context.Context, servers []driver.Server, candidates driver.Set) (*Agent, []string) {
	a := &Agent{
		servers:     make(map[string]driver.Server, len(servers)),
		drivers:     make(driver.Set),
		streams:     make(map[string]context.CancelFunc),
		offsiteLast: make(map[string]offsiteOutcome),
		watchers:    make(map[string]*players.Watcher),
		installing:  make(map[string]struct{}),
		ctx:         ctx,
	}

	var unavailable []string

	for _, s := range servers {
		a.servers[s.ID] = s

		/*
		 * 🚨 A bad player pattern must NOT stop the agent.
		 *
		 * The rest of this server still works — status, restarts, backups, the
		 * console — and refusing to start over a typo in one optional regex
		 * would take a whole host offline for a convenience feature. The
		 * mistake is reported and that server simply reports no players, which
		 * is what a server with no configuration does anyway.
		 */
		/*
		 * 🚨 NOT added to `unavailable`, which is about DRIVERS.
		 *
		 * It was, and the result was the same mistake reported twice in
		 * --check: once in the host-wide block where it read as informational,
		 * and once against the server where it read as a fault. Two lines for
		 * one problem, disagreeing about how serious it is, is worse than
		 * either alone. The per-server check re-runs players.New and reports it
		 * properly; this only has to not crash.
		 */
		w, err := players.New(s.Players)
		if err != nil {
			continue
		}

		if w != nil {
			a.watchers[s.ID] = w
		}
	}

	for name, d := range candidates {
		if err := d.Available(ctx); err != nil {
			unavailable = append(unavailable, fmt.Sprintf("%s (%v)", name, err))
			continue
		}
		a.drivers[name] = d
	}
	a.unavailable = unavailable

	return a, unavailable
}

// Provisioning tells the agent what may be installed and how to persist the
// result. Called by the command once its config is loaded; an agent without it
// simply reports no templates.
func (a *Agent) Provisioning(templates []provision.Template, register func(driver.Server) error) {
	a.templates = templates
	a.register = register
}

// Emit is how a long-running verb pushes events back to the forum.
type Emit func(protocol.Event)

// Handle answers one request. It never panics out to the caller and never
// returns a bare error: every failure is a protocol.Error with a code.
//
// 🚨 The first thing it does is check the verb against the closed set. A verb
// the agent does not implement is refused HERE, before any lookup, any driver,
// any side effect — which is the property the whole security argument rests
// on.
func (a *Agent) Handle(ctx context.Context, req protocol.Request, emit Emit) protocol.Response {
	res := protocol.Response{ID: req.ID}

	if !protocol.Known(req.Verb) {
		res.Error = protocol.Errf(protocol.CodeUnknownVerb, "no such verb %q", req.Verb)
		return res
	}

	var (
		srv driver.Server
		drv driver.Driver
	)
	if protocol.NeedsServer(req.Verb) {
		var ok bool
		srv, ok = a.server(req.Server)
		if !ok {
			res.Error = protocol.Errf(protocol.CodeUnknownServer, "no server %q on this agent", req.Server)
			return res
		}
		drv, ok = a.drivers[srv.Driver]
		if !ok {
			res.Error = protocol.Errf(protocol.CodeNotSupported,
				"server %q needs the %q driver, which is not available on this host", srv.ID, srv.Driver)
			return res
		}
	}

	data, err := a.dispatch(ctx, req, srv, drv, emit)
	if err != nil {
		if pe, ok := err.(*protocol.Error); ok {
			res.Error = pe
		} else {
			res.Error = protocol.Errf(protocol.CodeInternal, "%v", err)
		}
		return res
	}

	if data != nil {
		b, merr := json.Marshal(data)
		if merr != nil {
			res.Error = protocol.Errf(protocol.CodeInternal, "encoding result: %v", merr)
			return res
		}
		res.Data = b
	}
	res.OK = true
	return res
}

func (a *Agent) dispatch(ctx context.Context, req protocol.Request, srv driver.Server, drv driver.Driver, emit Emit) (any, error) {
	switch req.Verb {

	case protocol.VerbPing:
		return map[string]any{"pong": true, "at": time.Now().UTC()}, nil

	case protocol.VerbAgentInfo:
		verbs := make([]string, 0)
		for _, v := range protocol.Verbs() {
			verbs = append(verbs, string(v))
		}
		return protocol.AgentInfo{
			Version: Version,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Drivers: a.drivers.Names(),
			Verbs:   verbs,
			Servers: len(a.serverList()),
		}, nil

	case protocol.VerbServerList:
		servers := a.serverList()

		out := make([]map[string]string, 0, len(servers))
		for _, s := range servers {
			out = append(out, map[string]string{"id": s.ID, "name": s.Name, "driver": s.Driver})
		}
		return out, nil

	case protocol.VerbStatus:
		return drv.Status(ctx, srv)

	case protocol.VerbStart:
		if err := drv.Start(ctx, srv); err != nil {
			return nil, err
		}
		return drv.Status(ctx, srv)

	case protocol.VerbStop:
		grace, err := graceFrom(req, srv)
		if err != nil {
			return nil, err
		}
		if err := drv.Stop(ctx, srv, grace); err != nil {
			return nil, err
		}
		return drv.Status(ctx, srv)

	case protocol.VerbRestart:
		grace, err := graceFrom(req, srv)
		if err != nil {
			return nil, err
		}
		// A stop that fails because it was already stopped must not abort the
		// restart — "restart" means "be running afterwards".
		if err := drv.Stop(ctx, srv, grace); err != nil {
			if pe, ok := err.(*protocol.Error); !ok || pe.Code != protocol.CodeNotRunning {
				return nil, err
			}
		}
		if err := drv.Start(ctx, srv); err != nil {
			return nil, err
		}
		return drv.Status(ctx, srv)

	case protocol.VerbStats:
		return drv.Stats(ctx, srv)

	case protocol.VerbConsoleSend:
		var p protocol.SendParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if p.Line == "" {
			return nil, protocol.Errf(protocol.CodeBadRequest, "console.send needs a line")
		}
		if err := drv.Send(ctx, srv, p.Line); err != nil {
			return nil, err
		}
		return map[string]any{"sent": true}, nil

	case protocol.VerbBackupCreate:
		cfg, err := backupConfig(srv)
		if err != nil {
			return nil, err
		}

		b, err := backup.Create(ctx, cfg, srv.Name, false)
		if err != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", err)
		}

		// Retention runs after a successful create, never before: pruning
		// first would make room by deleting an old backup and then, if the new
		// one failed, leave the operator with fewer than they started with.
		removed, _ := backup.Prune(cfg)

		/*
		 * 🚨 The off-site copy happens AFTER the local one is complete, and its
		 * failure does not fail the backup.
		 *
		 * A local archive that exists is worth more than an upload that
		 * worked: the common disasters — a bad mod, a wiped world, a restore
		 * from the wrong save — are all recovered from the local copy, and the
		 * off-site one is for the rarer case of losing the host. Returning an
		 * error here because a bucket was unreachable would tell an operator
		 * their backup failed when it plainly did not, and the honest version
		 * of that sentence is what `offsite` in the result says instead.
		 */
		result := map[string]any{"backup": b, "pruned": removed}

		if srv.Offsite.Configured() {
			result["offsite"] = a.copyOffsite(ctx, srv, cfg, b)
		}

		return result, nil

	case protocol.VerbProvisionTemplates:
		return map[string]any{"templates": provision.List(a.templates)}, nil

	case protocol.VerbProvisionInstall:
		var p protocol.ProvisionParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		return a.provision(ctx, p, emit)

	case protocol.VerbPlayerVerify:
		var p protocol.VerifyParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		w := a.watcher(srv.ID)
		if w == nil {
			return nil, protocol.Errf(protocol.CodeNotSupported,
				"this server does not read who is playing, so it cannot verify anybody")
		}

		line, verr := w.VerifyLine(p.Player, p.Code)
		if verr != nil {
			// 🚨 CodeBadRequest, not CodeDriverFailed. Every refusal here is
			// the caller asking for something it may not have — a player who
			// is not in the game, a name shaped like a command. Reporting it
			// as a host failure would hide the one message that tells somebody
			// to go and join the server first.
			return nil, protocol.Errf(protocol.CodeBadRequest, "%v", verr)
		}

		if serr := drv.Send(ctx, srv, line); serr != nil {
			return nil, serr
		}

		/*
		 * 🚨 The rendered line is NOT returned. It contains the code, and a
		 * command result is readable by whoever queued it — which for this
		 * verb is the person being verified, so it would hand them the answer
		 * to the question they are meant to go and read in the game. The whole
		 * proof is that they saw it there.
		 */
		return map[string]any{"sent": true}, nil

	case protocol.VerbConfigList:
		return map[string]any{"files": settings.List(srv.Config)}, nil

	case protocol.VerbConfigGet:
		var p protocol.ConfigParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		file, ferr := settings.Find(srv.Config, p.File)
		if ferr != nil {
			return nil, protocol.Errf(protocol.CodeBadRequest, "%v", ferr)
		}

		set, rerr := settings.Read(file)
		if rerr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", rerr)
		}

		return set, nil

	case protocol.VerbConfigSet:
		var p protocol.ConfigParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		file, ferr := settings.Find(srv.Config, p.File)
		if ferr != nil {
			return nil, protocol.Errf(protocol.CodeBadRequest, "%v", ferr)
		}

		if werr := settings.Write(file, p.Section, p.Key, p.Value); werr != nil {
			/*
			 * 🚨 CodeBadRequest, not CodeDriverFailed. Every refusal from
			 * settings.Write is the caller asking for something it may not
			 * have — a read-only file, a key outside the allowlist, a value
			 * containing a newline, a key that does not exist. Reporting those
			 * as a driver failure would put "the host had a problem" in front
			 * of an operator whose actual problem is that they asked for the
			 * wrong thing, and the message explains which.
			 */
			return nil, protocol.Errf(protocol.CodeBadRequest, "%v", werr)
		}

		// 🚨 The file back, read fresh from disk. A panel that echoed the
		// value it just sent would show a successful save of something the
		// file may not actually contain — and the one thing worth being sure
		// of after editing a game config is what is now in it.
		set, rerr := settings.Read(file)
		if rerr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", rerr)
		}

		return set, nil

	case protocol.VerbBackupList:
		cfg, err := backupConfig(srv)
		if err != nil {
			return nil, err
		}

		list, lerr := backup.List(cfg)
		if lerr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", lerr)
		}

		return map[string]any{"backups": list}, nil

	case protocol.VerbBackupRestore:
		cfg, err := backupConfig(srv)
		if err != nil {
			return nil, err
		}

		var p protocol.BackupParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		st, serr := drv.Status(ctx, srv)
		if serr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "cannot determine whether the server is running: %v", serr)
		}

		/*
		 * 🚨 The RUNNING state is read here, not taken from the request.
		 * Letting the caller assert "it is stopped" would make the one check
		 * standing between a live server and a corrupted world a claim by
		 * whoever is asking.
		 */
		safety, rerr := backup.Restore(ctx, cfg, p.ID, st.State == protocol.StateRunning)
		if rerr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", rerr)
		}

		return map[string]any{"restored": p.ID, "safety": safety}, nil

	case protocol.VerbBackupDelete:
		cfg, err := backupConfig(srv)
		if err != nil {
			return nil, err
		}

		var p protocol.BackupParams
		if derr := decode(req.Params, &p); derr != nil {
			return nil, derr
		}

		if derr := backup.Delete(cfg, p.ID); derr != nil {
			return nil, protocol.Errf(protocol.CodeDriverFailed, "%v", derr)
		}

		return map[string]any{"deleted": p.ID}, nil

	case protocol.VerbConsoleTail:
		var p protocol.TailParams
		if err := decode(req.Params, &p); err != nil {
			return nil, err
		}
		if p.History <= 0 {
			p.History = 200
		}
		return a.tail(ctx, req, srv, drv, p, emit)
	}

	// Unreachable: Known() gates every verb before this point. Returning an
	// error rather than panicking means adding a verb to the set and
	// forgetting the case here is a clean refusal, not a dead agent.
	return nil, protocol.Errf(protocol.CodeInternal, "verb %q is known but not implemented", req.Verb)
}

func (a *Agent) tail(ctx context.Context, req protocol.Request, srv driver.Server, drv driver.Driver, p protocol.TailParams, emit Emit) (any, error) {
	if !p.Follow {
		// Bounded: read the scrollback, answer, done.
		var lines []protocol.Line
		if err := drv.Tail(ctx, srv, p.History, false, func(l protocol.Line) {
			lines = append(lines, l)
		}); err != nil {
			return nil, err
		}
		return map[string]any{"lines": lines}, nil
	}

	// Following: answer immediately so the caller is not left waiting, then
	// stream events under this request's ID until it is cancelled.
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	a.mu.Lock()
	if old, ok := a.streams[req.ID]; ok {
		old() // the same ID twice replaces the stream rather than duplicating it
	}
	a.streams[req.ID] = cancel
	a.mu.Unlock()

	a.background.Add(1)

	go func() {
		defer a.background.Done()

		defer func() {
			a.mu.Lock()
			delete(a.streams, req.ID)
			a.mu.Unlock()
			cancel()
		}()

		_ = drv.Tail(streamCtx, srv, p.History, true, func(l protocol.Line) {
			b, err := json.Marshal(l)
			if err != nil {
				return
			}
			emit(protocol.Event{Stream: req.ID, Kind: "console", At: l.At, Data: b})
		})
	}()

	return map[string]any{"streaming": true, "stream": req.ID}, nil
}

// CancelStream ends a following tail. The forum calls this when a console tab
// closes; without it an agent accumulates one `docker logs --follow` per tab
// anybody ever opened.
func (a *Agent) CancelStream(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cancel, ok := a.streams[id]
	if ok {
		cancel()
		delete(a.streams, id)
	}
	return ok
}

// CancelAll ends every stream, for a clean shutdown.
func (a *Agent) CancelAll() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, cancel := range a.streams {
		cancel()
		delete(a.streams, id)
	}
}

/*
Shutdown ends the streams and waits for everything the agent started in the
background to finish.

🚨 An install in flight is WAITED FOR, not abandoned. It is the one piece of
background work that writes files — the install directory, then the operator's
config — and a process that exits partway through that leaves the operator with
a config file that parses as neither the agent they had nor the one they were
getting.

How long that wait can be is set by the context the agent was built with, not
by this: cancelling it kills steamcmd and aborts the download, so the install
returns its error within moments. The command's signal handler does exactly
that, which is what makes this a wait of milliseconds rather than of hours.
*/
func (a *Agent) Shutdown() {
	a.CancelAll()
	a.background.Wait()
}

/*
provision installs a new server and adds it to this agent.

🚨 It returns as soon as the install STARTS, and reports progress as events.

A Steam download can be twenty gigabytes. Holding the command open until it
finishes would block the poll it arrived on, time out, be retried, and start a
second download of the same game into the same directory — so the obvious
synchronous implementation is not merely slow, it corrupts the thing it is
installing. The forum learns it began; the console panel shows steamcmd's own
output as it goes; the server appears when it is ready.
*/
func (a *Agent) provision(ctx context.Context, p protocol.ProvisionParams, emit Emit) (any, error) {
	tpl, err := provision.Find(a.templates, p.Template)
	if err != nil {
		return nil, protocol.Errf(protocol.CodeBadRequest, "%v", err)
	}

	server, err := tpl.Server(p.ID, p.Name)
	if err != nil {
		return nil, protocol.Errf(protocol.CodeBadRequest, "%v", err)
	}

	a.mu.Lock()
	_, clash := a.servers[server.ID]
	_, installing := a.installing[server.ID]
	a.mu.Unlock()

	if clash {
		return nil, protocol.Errf(protocol.CodeBadRequest, "a server called %q already exists on this host", server.ID)
	}

	/*
	 * 🚨 One install per id at a time.
	 *
	 * Two steamcmd runs writing the same directory produce a corrupt game that
	 * starts and then misbehaves in ways nobody traces back to here. A second
	 * click, an impatient retry or a duplicated command all land here, and
	 * refusing is both correct and the only thing that can be explained.
	 */
	if installing {
		return nil, protocol.Errf(protocol.CodeBadRequest, "%q is already being installed", server.ID)
	}

	a.mu.Lock()
	a.installing[server.ID] = struct{}{}
	a.mu.Unlock()

	/*
	 * 🚨 Counted into a.background BEFORE the goroutine starts, never inside
	 * it. An Add() that runs after the `go` races the Wait() it exists to be
	 * seen by, so a shutdown arriving in that window walks away from an
	 * install it was supposed to wait for — which is the bug itself, back.
	 */
	a.background.Add(1)

	go func() {
		defer a.background.Done()

		a.runInstall(server, tpl, emit)
	}()

	return map[string]any{"started": true, "server": server.ID}, nil
}

// runInstall does the download and registers the server, reporting as it goes.
func (a *Agent) runInstall(server driver.Server, tpl provision.Template, emit Emit) {
	defer func() {
		a.mu.Lock()
		delete(a.installing, server.ID)
		a.mu.Unlock()
	}()

	say := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)

		/*
		 * 🚨 Emitted as console output for the server BEING CREATED, so it
		 * lands in the panel somebody is already watching. A separate
		 * progress channel would be a second thing to build, a second thing to
		 * poll and a second place to look — and the console is where a person
		 * installing a game server expects installer output to be.
		 */
		emit(protocol.Event{
			Kind: "console",
			At:   time.Now(),
			Data: mustJSON(protocol.Line{Server: server.ID, At: time.Now(), Text: line}),
		})
	}

	say("installing %s as %s", tpl.ID, server.ID)

	/*
	 * 🚨 A context of the AGENT's, not the request's.
	 *
	 * The request that started this is answered already, and its context is
	 * cancelled the moment that poll completes — inheriting it would kill
	 * steamcmd within seconds, leaving a half-downloaded game and an operator
	 * with no idea why. Bounded rather than unbounded so a wedged installer
	 * cannot hold a slot for ever.
	 */
	ctx, cancel := context.WithTimeout(a.ctx, 6*time.Hour)
	defer cancel()

	if err := tpl.Install(ctx, server.ID, func(line string) { say("%s", line) }); err != nil {
		say("install failed: %v", err)

		return
	}

	if a.register == nil {
		say("installed, but this agent cannot persist new servers")

		return
	}

	/*
	 * 🚨 Persisted BEFORE it is served.
	 *
	 * A server added to memory and not to the file works perfectly until the
	 * next agent restart, when it silently disappears — along with any backups
	 * that were scheduled for it. Writing first means the two can only
	 * disagree in the safe direction.
	 */
	if err := a.register(server); err != nil {
		say("installed, but the server could not be saved: %v", err)

		return
	}

	w, werr := players.New(server.Players)

	/*
	 * 🚨 Both maps under ONE lock, and the watcher under a lock at all.
	 *
	 * This runs on the install's own goroutine, and the very next poll reads
	 * both maps from another. The servers map was already guarded here and the
	 * watchers map was not, which is the worse half of the same bug: a poll
	 * landing in that window reads a map mid-write and the Go runtime stops
	 * the process outright. Taken together so the two can never be seen
	 * disagreeing either — a server in the list whose watcher has not arrived
	 * yet reports an empty game for one poll.
	 */
	a.mu.Lock()
	a.servers[server.ID] = server

	if werr == nil && w != nil {
		a.watchers[server.ID] = w
	}
	a.mu.Unlock()

	say("%s is installed and ready to start", server.ID)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return b
}

// offsiteStatus reports what this agent knows about remote copies for a server.
func (a *Agent) offsiteStatus(s driver.Server) *protocol.OffsiteStatus {
	if !s.Offsite.Configured() {
		return nil
	}

	status := &protocol.OffsiteStatus{Configured: true, Bucket: s.Offsite.Bucket}

	a.offsiteMu.Lock()
	last, ok := a.offsiteLast[s.ID]
	a.offsiteMu.Unlock()

	if ok {
		status.LastAt = &last.at
		status.LastOK = last.ok
		status.LastError = last.err
	}

	return status
}

// rememberOffsite records the outcome of one copy.
type offsiteOutcome struct {
	at  time.Time
	ok  bool
	err string
}

func (a *Agent) rememberOffsite(serverID string, ok bool, errText string) {
	a.offsiteMu.Lock()
	defer a.offsiteMu.Unlock()

	if a.offsiteLast == nil {
		a.offsiteLast = map[string]offsiteOutcome{}
	}

	a.offsiteLast[serverID] = offsiteOutcome{at: time.Now().UTC(), ok: ok, err: errText}
}

// copyOffsite uploads one finished archive and reports how it went.
//
// 🚨 Never returns an error, because none of its callers should fail on one.
// The shape it returns is what the forum shows: "copied", or "not copied and
// here is the provider's own reason". An operator debugging bucket credentials
// needs "SignatureDoesNotMatch" or "NoSuchBucket", not "off-site failed".
func (a *Agent) copyOffsite(ctx context.Context, srv driver.Server, cfg backup.Config, b *backup.Backup) map[string]any {
	store, err := offsite.New(srv.Offsite)
	if err != nil {
		a.rememberOffsite(srv.ID, false, err.Error())

		return map[string]any{"ok": false, "error": err.Error()}
	}

	/*
	 * 🚨 A generous timeout, from a context of its own.
	 *
	 * The caller's context is the command's, and a command is expected to
	 * answer within a poll window — but uploading several gigabytes over a
	 * home connection legitimately takes much longer than that. Inheriting the
	 * command's deadline would make off-site copies work in testing with a
	 * small world and silently stop the moment one got real.
	 *
	 * Bounded rather than unbounded so a hung provider cannot pin a goroutine
	 * and a file handle for ever.
	 */
	uploadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Hour)
	defer cancel()

	path := filepath.Join(cfg.Dir, b.ID)

	if err := store.Put(uploadCtx, b.ID, path); err != nil {
		a.rememberOffsite(srv.ID, false, err.Error())

		return map[string]any{"ok": false, "error": err.Error()}
	}

	a.rememberOffsite(srv.ID, true, "")

	out := map[string]any{"ok": true}

	if pruned, perr := store.Prune(uploadCtx); perr != nil {
		// Reported alongside a successful upload, because they are separate
		// facts: the copy is safely off-site AND retention is not working.
		out["pruneError"] = perr.Error()
	} else if len(pruned) > 0 {
		out["pruned"] = pruned
	}

	return out
}

// backupConfig builds a backup config from what the OPERATOR configured.
func backupConfig(srv driver.Server) (backup.Config, error) {
	if srv.BackupRoot == "" || len(srv.BackupPaths) == 0 {
		return backup.Config{}, protocol.Errf(protocol.CodeNotSupported,
			"server %q has no backup paths configured on this host", srv.ID)
	}

	dir := srv.BackupDir
	if dir == "" {
		dir = srv.BackupRoot + "/garrison-backups"
	}

	return backup.Config{
		Dir:   dir,
		Root:  srv.BackupRoot,
		Paths: srv.BackupPaths,
		Keep:  srv.BackupKeep,
	}, nil
}

func graceFrom(req protocol.Request, srv driver.Server) (time.Duration, error) {
	grace := srv.Grace()
	if len(req.Params) == 0 {
		return grace, nil
	}
	var p protocol.StopParams
	if err := decode(req.Params, &p); err != nil {
		return 0, err
	}
	if p.GraceSeconds > 0 {
		grace = time.Duration(p.GraceSeconds) * time.Second
	}
	// 🚨 No path to zero. A caller asking for a grace of 0 wants a kill, and
	// a kill mid-save is a corrupt world. If forcing is ever wanted it needs
	// its own verb, with its own confirmation, not a number that looks like
	// a tuning knob.
	if p.GraceSeconds < 0 {
		return 0, protocol.Errf(protocol.CodeBadRequest, "graceSeconds cannot be negative")
	}
	return grace, nil
}

func decode(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return protocol.Errf(protocol.CodeBadRequest, "bad params: %v", err)
	}
	return nil
}

// Drivers lists the drivers that worked on this host, for --check and for
// agent.info.
func (a *Agent) Drivers() []string { return a.drivers.Names() }

// StatusAll reports every server this agent knows about.
//
// 🚨 Gathered on every poll, not on request. The forum's status page then
// renders from one cached row per server with no round trip to a host that
// might be asleep — which is what lets ten widgets on a page cost one query
// instead of ten requests.
func (a *Agent) StatusAll(ctx context.Context) []protocol.Status {
	servers := a.serverList()

	out := make([]protocol.Status, 0, len(servers))

	for _, s := range servers {
		drv, ok := a.drivers[s.Driver]
		if !ok {
			out = append(out, protocol.Status{
				Server: s.ID, Name: s.Name, Driver: s.Driver, State: protocol.StateUnknown,
				Detail: "driver not available on this host",
			})
			continue
		}

		st, err := drv.Status(ctx, s)
		st.Game = s.Game
		st.Name = s.Name
		if err != nil {
			st = protocol.Status{Server: s.ID, Name: s.Name, Driver: s.Driver, Game: s.Game, State: protocol.StateUnknown}
			if pe, isProto := err.(*protocol.Error); isProto {
				st.Detail = pe.Message
			}
		}

		// Stats ride along with status: a separate verb per server per poll
		// would triple the traffic for a number the page always shows anyway.
		if st.State == protocol.StateRunning {
			if stats, serr := drv.Stats(ctx, s); serr == nil {
				st.Stats = &stats
			}
		}

		/*
		 * 🚨 Health is evaluated on EVERY poll, not on request.
		 *
		 * The failure this exists to catch is silent: a server that is up,
		 * saving its world, refreshing its lobby, and unjoinable. Nobody goes
		 * looking for that — they find out when a player complains, which on
		 * the outage that prompted this was twenty hours later. A check that
		 * has to be asked for is a check nobody runs.
		 */
		st.Health = health.Check(ctx, st.State == protocol.StateRunning, s.Health, func(c context.Context, n int) ([]string, error) {
			var lines []string
			err := drv.Tail(c, s, n, false, func(l protocol.Line) {
				lines = append(lines, l.Text)
			})
			return lines, err
		})

		st.Backups = backupsFor(s)
		st.Offsite = a.offsiteStatus(s)

		if w := a.watcher(s.ID); w != nil {
			/*
			 * 🚨 A server that is not running has NOBODY in it, and the set is
			 * cleared rather than reported stale.
			 *
			 * The watcher's set is built from log lines, and a crashed server
			 * prints no goodbyes — so without this, a crash leaves everybody
			 * who was playing listed as still playing, on the panel somebody
			 * opened precisely because the server went down. It would also
			 * quietly inflate their playtime for as long as it stayed down.
			 */
			if st.State != protocol.StateRunning {
				w.Reset()
			}

			st.Players = w.Online()
			st.PlayersKnown = true
		}

		out = append(out, st)
	}
	return out
}

// MaxBackupsShipped bounds how many archives ride along with a status report.
//
// 🚨 Bounded for the same reason console output is. A server configured to keep
// every backup forever accumulates thousands of files, and an unbounded list
// would grow the poll body without limit until the request times out — taking
// every OTHER server's status down with it, because they share the poll. The
// most recent handful is what an operator restores from; the rest is history
// they would go to the host for anyway.
const MaxBackupsShipped = 25

// backupsFor lists a server's archives, newest first, for the status report.
//
// 🚨 Every failure here is SILENT ON PURPOSE, and this is the one place in the
// agent where that is right. A server with no backup paths configured is the
// normal case, not an error; an unreadable directory is worth knowing about but
// is not worth failing a status report over. If listing backups could fail a
// poll, one misconfigured server would stop the forum hearing about the state
// or health of every other server on the host — trading the feature this
// product exists for against a convenience panel.
func backupsFor(s driver.Server) []protocol.Backup {
	cfg, err := backupConfig(s)
	if err != nil {
		return nil
	}

	list, err := backup.List(cfg)
	if err != nil || len(list) == 0 {
		return nil
	}

	if len(list) > MaxBackupsShipped {
		list = list[:MaxBackupsShipped]
	}

	out := make([]protocol.Backup, 0, len(list))
	for _, b := range list {
		out = append(out, protocol.Backup{ID: b.ID, Size: b.Size, At: b.At, Safety: b.Safety})
	}

	return out
}
