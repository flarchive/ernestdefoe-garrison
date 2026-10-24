package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/ernestdefoe/garrison/internal/protocol"
)

// Link is the agent's connection to the forum.
//
// 🚨 The agent DIALS OUT. The forum never connects to the game host, which is
// why a game host needs no inbound port, no port forward, no static IP and no
// certificate of its own — and why somebody can run the Minecraft server on
// the box under their desk and still have it appear in the panel. That is the
// normal case for a forum community and the case a conventional panel handles
// worst.
//
// It also means the security posture is better by default: there is no
// listening socket on the game host to find, and the forum is the only thing
// exposed, which it already was.
type Link struct {
	url    string
	token  string
	agent  *Agent
	log    *slog.Logger
	dialer func(ctx context.Context, url string, opts *websocket.DialOptions) (*websocket.Conn, *http.Response, error)

	writeMu sync.Mutex
	conn    *websocket.Conn
}

// NewLink builds a link. url is the forum's agent endpoint (ws:// or wss://).
func NewLink(url, token string, a *Agent, log *slog.Logger) *Link {
	return &Link{url: url, token: token, agent: a, log: log, dialer: websocket.Dial}
}

// Run dials, serves, and redials for as long as ctx lives.
//
// 🚨 It never gives up. An agent that stops reconnecting after N attempts is
// an agent that is permanently offline after one overnight network blip, on a
// machine whose owner is asleep — and the first anybody knows is that their
// server cannot be restarted.
func (l *Link) Run(ctx context.Context) error {
	backoff := time.Second

	for ctx.Err() == nil {
		err := l.once(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			l.log.Warn("link dropped", "err", err, "retry_in", backoff.Round(time.Millisecond))
		}

		// Jittered, capped. Without jitter, every agent a forum owns redials
		// in lockstep after an outage and the first thing the recovering
		// forum sees is a thundering herd of its own making.
		jitter := time.Duration(rand.Int64N(int64(backoff / 2)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff/2 + jitter):
		}

		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	return ctx.Err()
}

func (l *Link) once(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	conn, _, err := l.dialer(dialCtx, l.url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + l.token}},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// Game server logs are chatty and a console tail replays scrollback on
	// subscribe; the default 32KiB read limit truncates a long Java stack
	// trace into a protocol error and drops the whole connection.
	conn.SetReadLimit(4 << 20)

	l.writeMu.Lock()
	l.conn = conn
	l.writeMu.Unlock()

	defer func() {
		l.agent.CancelAll()
		l.writeMu.Lock()
		l.conn = nil
		l.writeMu.Unlock()
		_ = conn.CloseNow()
	}()

	l.log.Info("connected", "url", l.url)

	connCtx, cancelConn := context.WithCancel(ctx)
	defer cancelConn()

	for {
		_, data, err := conn.Read(connCtx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		var frame protocol.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			l.log.Warn("unparseable frame", "err", err)
			continue
		}
		if frame.Request == nil {
			continue // responses and events flow the other way
		}

		// 🚨 One goroutine per request. A console tail that follows would
		// otherwise hold the read loop and the agent would answer nothing
		// else for as long as somebody had a console open.
		go func(req protocol.Request) {
			res := l.agent.Handle(connCtx, req, l.emit)
			if err := l.send(connCtx, protocol.Frame{Response: &res}); err != nil {
				l.log.Warn("could not answer", "id", req.ID, "err", err)
			}
		}(*frame.Request)
	}
}

func (l *Link) emit(ev protocol.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.send(ctx, protocol.Frame{Event: &ev}); err != nil {
		l.log.Debug("dropped event", "stream", ev.Stream, "err", err)
	}
}

func (l *Link) send(ctx context.Context, f protocol.Frame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}

	// 🚨 Serialised, and the lock is taken ONCE for the read of l.conn and the
	// write together. websocket.Conn allows one writer at a time, and with a
	// goroutine per request plus a stream emitting console lines there are
	// several. Releasing the lock between reading the connection and writing
	// to it would let a reconnect swap it underneath a writer — frames
	// interleave, and the connection dies with a protocol error that reads as
	// a network fault.
	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	if l.conn == nil {
		return errors.New("not connected")
	}
	return l.conn.Write(ctx, websocket.MessageText, b)
}
