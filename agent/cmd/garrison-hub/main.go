// Command garrison-hub stands in for the forum during phase 0.
//
// 🚨 This is NOT the product. Flarum will hold the agent connection in phase 1;
// this exists so the spike can be driven and, more importantly, so the verb set
// and the drivers can be exercised end to end before any PHP is written. If the
// protocol is awkward to speak from here, it will be worse from PHP.
//
// It accepts one agent connection, then reads commands from stdin:
//
//	info
//	list
//	status <server>
//	start <server>
//	stop <server> [graceSeconds]
//	restart <server> [graceSeconds]
//	stats <server>
//	logs <server> [lines]
//	follow <server>
//	send <server> <line...>
//	raw <verb> [server]        — deliberately allows an unknown verb, to prove
//	                             the agent refuses it
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/ernestdefoe/garrison/internal/protocol"
)

type hub struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	pending map[string]chan protocol.Response
	nextID  int
	ready   chan struct{}
	once    sync.Once
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address for the agent to dial")
	token := flag.String("token", "spike", "token the agent must present")
	wait := flag.Duration("wait", 30*time.Second, "how long to wait for an agent")
	flag.Parse()

	h := &hub{pending: map[string]chan protocol.Response{}, ready: make(chan struct{})}

	mux := http.NewServeMux()
	mux.HandleFunc("/agent", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+*token {
			http.Error(w, "unauthorised", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(4 << 20)
		h.serve(r.Context(), c)
	})

	srv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, "listen:", err)
			os.Exit(1)
		}
	}()
	fmt.Fprintf(os.Stderr, "hub listening on ws://%s/agent\n", *addr)

	select {
	case <-h.ready:
		fmt.Fprintln(os.Stderr, "agent connected")
	case <-time.After(*wait):
		fmt.Fprintln(os.Stderr, "no agent connected")
		os.Exit(1)
	}

	h.repl()
}

func (h *hub) serve(ctx context.Context, c *websocket.Conn) {
	h.mu.Lock()
	h.conn = c
	h.mu.Unlock()
	h.once.Do(func() { close(h.ready) })

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var f protocol.Frame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}

		switch {
		case f.Response != nil:
			h.mu.Lock()
			ch, ok := h.pending[f.Response.ID]
			delete(h.pending, f.Response.ID)
			h.mu.Unlock()
			if ok {
				ch <- *f.Response
			}

		case f.Event != nil:
			// Console lines arriving unsolicited — the half of the protocol a
			// request/response-only design could not carry.
			var l protocol.Line
			if err := json.Unmarshal(f.Event.Data, &l); err == nil {
				marker := " "
				if l.Stderr {
					marker = "!"
				}
				fmt.Printf("%s %s %s\n", marker, l.At.Format("15:04:05"), l.Text)
			}
		}
	}
}

func (h *hub) call(verb protocol.Verb, server string, params any) (protocol.Response, error) {
	h.mu.Lock()
	h.nextID++
	id := "r" + strconv.Itoa(h.nextID)
	ch := make(chan protocol.Response, 1)
	h.pending[id] = ch
	conn := h.conn
	h.mu.Unlock()

	req := protocol.Request{ID: id, Verb: verb, Server: server}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return protocol.Response{}, err
		}
		req.Params = b
	}

	b, err := json.Marshal(protocol.Frame{Request: &req})
	if err != nil {
		return protocol.Response{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return protocol.Response{}, err
	}

	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		return protocol.Response{}, fmt.Errorf("timed out waiting for %s", verb)
	}
}

func (h *hub) repl() {
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprint(os.Stderr, "garrison> ")
		if !sc.Scan() {
			return
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "quit" || line == "exit" {
			return
		}

		f := strings.Fields(line)
		cmd, args := f[0], f[1:]

		var (
			verb   protocol.Verb
			server string
			params any
		)

		switch cmd {
		case "info":
			verb = protocol.VerbAgentInfo
		case "ping":
			verb = protocol.VerbPing
		case "list":
			verb = protocol.VerbServerList
		case "status", "start", "stats":
			if len(args) < 1 {
				fmt.Println("usage:", cmd, "<server>")
				continue
			}
			verb = map[string]protocol.Verb{
				"status": protocol.VerbStatus,
				"start":  protocol.VerbStart,
				"stats":  protocol.VerbStats,
			}[cmd]
			server = args[0]
		case "stop", "restart":
			if len(args) < 1 {
				fmt.Println("usage:", cmd, "<server> [graceSeconds]")
				continue
			}
			verb = protocol.VerbStop
			if cmd == "restart" {
				verb = protocol.VerbRestart
			}
			server = args[0]
			if len(args) > 1 {
				n, _ := strconv.Atoi(args[1])
				params = protocol.StopParams{GraceSeconds: n}
			}
		case "logs", "follow":
			if len(args) < 1 {
				fmt.Println("usage:", cmd, "<server> [lines]")
				continue
			}
			verb = protocol.VerbConsoleTail
			server = args[0]
			n := 50
			if len(args) > 1 {
				n, _ = strconv.Atoi(args[1])
			}
			params = protocol.TailParams{History: n, Follow: cmd == "follow"}
		case "send":
			if len(args) < 2 {
				fmt.Println("usage: send <server> <line...>")
				continue
			}
			verb = protocol.VerbConsoleSend
			server = args[0]
			params = protocol.SendParams{Line: strings.Join(args[1:], " ")}
		case "raw":
			// The point of this command: hand the agent a verb it does not
			// have and watch it refuse. `raw shell.exec valheim` is the whole
			// security argument in one line.
			if len(args) < 1 {
				fmt.Println("usage: raw <verb> [server]")
				continue
			}
			verb = protocol.Verb(args[0])
			if len(args) > 1 {
				server = args[1]
			}
		default:
			fmt.Println("unknown command:", cmd)
			continue
		}

		res, err := h.call(verb, server, params)
		if err != nil {
			fmt.Println("ERROR:", err)
			continue
		}
		if !res.OK {
			fmt.Printf("REFUSED [%s] %s\n", res.Error.Code, res.Error.Message)
			continue
		}
		var pretty any
		_ = json.Unmarshal(res.Data, &pretty)
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Println(string(b))
	}
}
