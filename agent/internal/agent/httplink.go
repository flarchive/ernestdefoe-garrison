package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/ernestdefoe/garrison/internal/protocol"
)

// HTTPLink is the agent's link to a Flarum forum.
//
// 🚨 This is the transport the product ships with, and Link (websocket) is the
// optional upgrade — not the other way round.
//
// A websocket is nicer. It is also a daemon on the forum host, because PHP
// cannot hold a socket open: a request handler is not a server. Requiring one
// would mean Garrison could not be installed with `composer require`, would
// not run on shared hosting at all, and every support thread would start with
// "is the gateway running?". Flarum's whole appeal is that it runs anywhere.
//
// So the agent long-polls ordinary HTTPS. It still DIALS OUT — the property
// that matters, the one that means a game host needs no inbound port — and it
// carries its own status report up on the same request that asks for work, so
// a healthy idle agent costs one round trip per poll window rather than three.
type HTTPLink struct {
	url    string
	token  string
	agent  *Agent
	log    *slog.Logger
	client *http.Client

	mu      sync.Mutex
	results []result
	events  []protocol.Event

	console *consoleShipper
}

type result struct {
	ID           int    `json:"id"`
	OK           bool   `json:"ok"`
	Data         any    `json:"data,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type pollRequest struct {
	Info    protocol.AgentInfo `json:"info"`
	Servers []protocol.Status  `json:"servers"`
	Results []result           `json:"results,omitempty"`
	Events  []protocol.Event   `json:"events,omitempty"`

	// Console output since the last poll, for every server. Shipped
	// continuously rather than on subscription — see console.go.
	Console []protocol.Line `json:"console,omitempty"`
}

type pollCommand struct {
	ID     int             `json:"id"`
	Verb   protocol.Verb   `json:"verb"`
	Server string          `json:"server"`
	Params json.RawMessage `json:"params"`
}

type pollResponse struct {
	Commands    []pollCommand `json:"commands"`
	PollSeconds int           `json:"pollSeconds"`
}

// NewHTTPLink builds the polling link. url is the forum's poll endpoint.
func NewHTTPLink(url, token string, a *Agent, log *slog.Logger) *HTTPLink {
	return &HTTPLink{
		url:     url,
		token:   token,
		agent:   a,
		log:     log,
		console: newConsoleShipper(),
		client: &http.Client{
			// 🚨 Longer than the forum's poll window, and by a margin. A
			// client timeout shorter than the server's hold turns every idle
			// poll into an error, and the agent spends its life reconnecting
			// while looking, in the log, like a network problem.
			Timeout: 90 * time.Second,
		},
	}
}

// Run polls until ctx ends.
func (l *HTTPLink) Run(ctx context.Context) error {
	backoff := time.Second

	for ctx.Err() == nil {
		n, err := l.once(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			l.log.Warn("poll failed", "err", err, "retry_in", backoff.Round(time.Millisecond))

			jitter := time.Duration(rand.Int64N(int64(backoff/2) + 1))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff/2 + jitter):
			}

			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}

		backoff = time.Second

		// 🚨 When the forum returned work, go straight back round without
		// pausing. A queue of three commands must not take three poll windows
		// to drain — that is what makes a panel feel broken even though every
		// individual action succeeded.
		if n == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return ctx.Err()
}

func (l *HTTPLink) once(ctx context.Context) (int, error) {
	/*
	 * 🚨 Console FIRST, then status.
	 *
	 * The console read is also what feeds the player watcher — the same lines,
	 * read once. Gathering status first would report a set of players built
	 * from the PREVIOUS poll's lines, so somebody who joined twenty seconds ago
	 * would not appear until the poll after next. Half a minute of "not online"
	 * for somebody who is standing in the game is exactly the kind of small lie
	 * that makes a panel feel unreliable.
	 */
	console := l.console.collect(ctx, l.agent)

	body := pollRequest{
		Info: protocol.AgentInfo{
			Version: Version,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
			Drivers: l.agent.Drivers(),
		},
		Servers: l.agent.StatusAll(ctx),
		Console: console,
	}

	l.mu.Lock()
	body.Results, l.results = l.results, nil
	body.Events, l.events = l.events, nil
	l.mu.Unlock()

	buf, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url, bytes.NewReader(buf))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := l.client.Do(req)
	if err != nil {
		// 🚨 Anything unsent goes back on the queue. Dropping a result on a
		// network error leaves a command "delivered" for ever on the forum,
		// which shows as in-progress and makes an operator wait for something
		// that already finished.
		l.requeue(body.Results, body.Events)
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusUnauthorized {
		// Not retryable by waiting. Say so once, clearly, rather than looping
		// forever on a token that will never work.
		return 0, fmt.Errorf("the forum rejected this agent's token")
	}
	if res.StatusCode != http.StatusOK {
		l.requeue(body.Results, body.Events)
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 256))
		return 0, fmt.Errorf("forum returned %d: %s", res.StatusCode, bytes.TrimSpace(snippet))
	}

	var out pollResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decoding poll response: %w", err)
	}

	for _, c := range out.Commands {
		l.run(ctx, c)

		/*
		 * 🚨 After a restart, forget what console output was already shipped.
		 *
		 * The lines worth reading are the ones explaining why the server went
		 * down, and they are exactly the ones the shipper would skip as
		 * already seen — a restart replaces the log, so "the last line I sent"
		 * no longer appears in it and the whole window reads as new anyway.
		 * Forgetting makes that explicit rather than accidental.
		 */
		if c.Verb == protocol.VerbRestart || c.Verb == protocol.VerbStart {
			l.console.forget(c.Server)
		}
	}

	return len(out.Commands), nil
}

func (l *HTTPLink) requeue(results []result, events []protocol.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.results = append(results, l.results...)

	// Events are bounded: console output is endless, and an agent that cannot
	// reach its forum for an hour must not spend that hour filling memory with
	// lines nobody will ever read.
	l.events = append(events, l.events...)
	if len(l.events) > 5000 {
		l.events = l.events[len(l.events)-5000:]
	}
}

func (l *HTTPLink) run(ctx context.Context, c pollCommand) {
	res := l.agent.Handle(ctx, protocol.Request{
		ID:     fmt.Sprint(c.ID),
		Verb:   c.Verb,
		Server: c.Server,
		Params: c.Params,
	}, l.emit)

	r := result{ID: c.ID, OK: res.OK}
	if res.Error != nil {
		r.ErrorCode = res.Error.Code
		r.ErrorMessage = res.Error.Message
	}
	if len(res.Data) > 0 {
		var data any
		if err := json.Unmarshal(res.Data, &data); err == nil {
			r.Data = data
		}
	}

	l.mu.Lock()
	l.results = append(l.results, r)
	l.mu.Unlock()
}

func (l *HTTPLink) emit(ev protocol.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	if len(l.events) > 5000 {
		l.events = l.events[len(l.events)-5000:]
	}
}
