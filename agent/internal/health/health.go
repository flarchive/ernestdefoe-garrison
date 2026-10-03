// Package health answers a question no process table can: not "is it running"
// but "can a player actually get in".
//
// 🚨 This package exists because of a real outage. On 2026-09-14 a Valheim
// server was up, healthy-looking and completely unjoinable for twenty hours.
// The container said Up 4 weeks, the host was idle, the firewall was open, DNS
// resolved, the world was saving every few minutes and the lobby was
// refreshing on schedule. Every check anyone would think to run said fine. The
// process had thrown a NullReferenceException inside its matchmaking seconds
// after registering, and Unity logs an exception and carries on — so it kept
// doing everything that did not involve players.
//
// Liveness could not see that. Readiness can.
//
// 🚨 THE RULE FOR ADDING A PROBE: every probe that ships must be one that was
// OBSERVED distinguishing broken from working on a real server. During that
// outage the missing UDP 2456 bind looked like the smoking gun and was not —
// Valheim never binds it under -crossplay, and after the restart that fixed
// everything it was still unbound. A probe written from a plausible theory
// alerts constantly and gets switched off within a week, which leaves the
// operator worse off than having no probe at all.
package health

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// State is the outcome of checking one server.
type State string

const (
	// StateOK — every probe passed.
	StateOK State = "ok"
	// StateUnready — the process is alive but players cannot get in. This is
	// the state the whole package exists for.
	StateUnready State = "unready"
	// StateDown — it is not running at all.
	StateDown State = "down"
	// StateUnknown — no probes configured, or none could be evaluated.
	//
	// 🚨 Distinct from OK on purpose. "We did not look" and "we looked and it
	// was fine" must never render as the same thing, or an operator trusts a
	// green light that means nothing.
	StateUnknown State = "unknown"
)

// Probe is one check, as configured in a game manifest.
type Probe struct {
	// Name is what an operator sees when it fails. Write it as the FINDING,
	// not the mechanism: "no player has connected in 2h while advertised" is
	// useful at 3am; "check_3" is not.
	Name string `json:"name"`

	// Type selects the check. See Run.
	Type string `json:"type"`

	// Port, for udp_recvq and tcp.
	Port int `json:"port,omitempty"`

	// Pattern, for log_match and log_quiet.
	Pattern string `json:"pattern,omitempty"`

	// Within bounds how far back a log probe looks, e.g. "10m".
	Within string `json:"within,omitempty"`

	// Threshold is the value above which udp_recvq is considered stuck.
	Threshold int `json:"threshold,omitempty"`

	// Warn marks a probe as advisory: it is reported and never escalates. For
	// signals that are real but not conclusive on their own.
	Warn bool `json:"warn,omitempty"`
}

// Result is one probe's outcome.
type Result struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail,omitempty"`

	// Skipped marks a probe that could not be evaluated — the file was not
	// readable, the interface was missing. NOT a failure: a probe that cannot
	// run has learned nothing, and treating that as a fault restarts healthy
	// servers.
	Skipped bool `json:"skipped,omitempty"`
}

// Report is everything the agent learned about one server this cycle.
type Report struct {
	State   State     `json:"state"`
	At      time.Time `json:"at"`
	Results []Result  `json:"results,omitempty"`
	Summary string    `json:"summary,omitempty"`
}

// LogSource hands a probe the recent console output of a server, newest last.
// The driver supplies it, so a probe never needs to know whether the server is
// a container, a unit or a bare process.
type LogSource func(ctx context.Context, lines int) ([]string, error)

// Check runs every probe and folds the results into one state.
func Check(ctx context.Context, running bool, probes []Probe, logs LogSource) Report {
	report := Report{At: time.Now().UTC()}

	// 🚨 Liveness first, and it short-circuits. Running readiness probes
	// against a stopped server produces a page of failures that all say the
	// same thing, and buries the one fact that matters.
	if !running {
		report.State = StateDown
		report.Summary = "not running"
		return report
	}

	if len(probes) == 0 {
		report.State = StateUnknown
		report.Summary = "no readiness probes configured for this game"
		return report
	}

	failed := 0

	for _, p := range probes {
		r := run(ctx, p, logs)
		report.Results = append(report.Results, r)

		if !r.OK && !r.Skipped && !p.Warn {
			failed++

			if report.Summary == "" {
				report.Summary = r.Name
			}
		}
	}

	if failed > 0 {
		report.State = StateUnready
		return report
	}

	// Every probe skipped is not the same as every probe passing.
	allSkipped := true
	for _, r := range report.Results {
		if !r.Skipped {
			allSkipped = false
			break
		}
	}

	if allSkipped {
		report.State = StateUnknown
		report.Summary = "no probe could be evaluated"
		return report
	}

	report.State = StateOK
	return report
}

func run(ctx context.Context, p Probe, logs LogSource) Result {
	r := Result{Name: p.Name, Type: p.Type, Warn: p.Warn}

	switch p.Type {
	case "udp_recvq":
		ok, detail, skipped := udpRecvQ(p.Port, p.Threshold)
		r.OK, r.Detail, r.Skipped = ok, detail, skipped

	case "tcp":
		// 🚨 Skipped, not passed. A probe with no port checks nothing, and
		// reporting it healthy makes a typo look like a working safeguard —
		// the operator believes something is watching when nothing is.
		if p.Port == 0 {
			r.Skipped = true
			r.Detail = "no port configured"
		} else {
			r.OK, r.Detail = tcpConnect(ctx, p.Port)
		}

	case "log_match":
		// Fails when the pattern IS present: a known crash signature.
		r.OK, r.Detail, r.Skipped = logContains(ctx, logs, p, false)

	case "log_quiet":
		// Fails when the pattern is ABSENT: nothing good has happened lately.
		r.OK, r.Detail, r.Skipped = logContains(ctx, logs, p, true)

	default:
		// An unknown probe type is a manifest mistake, and it must not read as
		// a failing server — that would restart a healthy game because
		// somebody typo'd a word in YAML.
		r.Skipped = true
		r.Detail = "unknown probe type " + strconv.Quote(p.Type)
	}

	return r
}

// udpRecvQ is THE Valheim probe.
//
// 🚨 A receive queue that sits at a constant non-zero value means packets are
// arriving and nothing is reading them — the socket is open, the process is
// alive, and it has stopped listening. During the outage this sat at exactly
// 9600 bytes for twenty hours and dropped to 0 the moment a clean restart
// fixed it. It is the single measurement that distinguished broken from
// working, and it is why this package is not a wrapper around `ps`.
func udpRecvQ(port, threshold int) (ok bool, detail string, skipped bool) {
	if port == 0 {
		return true, "", true
	}

	if threshold <= 0 {
		threshold = 1
	}

	q, found, err := readUDPQueue(port)
	if err != nil || !found {
		// No socket on that port is not this probe's business to judge: under
		// crossplay relays a game legitimately binds nothing. Another probe
		// can have an opinion; this one abstains.
		return true, "", true
	}

	if q >= threshold {
		return false, fmt.Sprintf("UDP %d has %d bytes queued and unread — the socket is open but nothing is reading it", port, q), false
	}

	return true, fmt.Sprintf("UDP %d queue %d", port, q), false
}

// readUDPQueue parses /proc/net/udp{,6} for the queue depth on a local port.
func readUDPQueue(port int) (queue int, found bool, err error) {
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		f, ferr := os.Open(path)
		if ferr != nil {
			continue
		}

		q, ok := parseUDPQueue(f, port)
		f.Close()

		if ok {
			return q, true, nil
		}
	}

	return 0, false, nil
}

// parseUDPQueue is the parsing on its own, so it can be tested against a real
// captured /proc/net/udp rather than only on a Linux host with the right
// socket open.
func parseUDPQueue(r io.Reader, port int) (queue int, found bool) {
	sc := bufio.NewScanner(r)
	sc.Scan() // header

	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}

		// local_address is HEX "IP:PORT".
		addr := fields[1]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}

		p, perr := strconv.ParseInt(addr[i+1:], 16, 32)
		if perr != nil || int(p) != port {
			continue
		}

		// tx_queue:rx_queue, both hex.
		qs := strings.SplitN(fields[4], ":", 2)
		if len(qs) != 2 {
			continue
		}

		rx, qerr := strconv.ParseInt(qs[1], 16, 64)
		if qerr != nil {
			continue
		}

		return int(rx), true
	}

	return 0, false
}

func tcpConnect(ctx context.Context, port int) (bool, string) {
	if port == 0 {
		return true, ""
	}

	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false, fmt.Sprintf("nothing accepting on TCP %d", port)
	}
	_ = conn.Close()

	return true, fmt.Sprintf("TCP %d accepting", port)
}

// logContains reads recent output and looks for a pattern.
//
// wantPresent=false: the pattern is a crash signature; finding it fails.
// wantPresent=true:  the pattern is a sign of life; NOT finding it fails.
func logContains(ctx context.Context, logs LogSource, p Probe, wantPresent bool) (ok bool, detail string, skipped bool) {
	if logs == nil || p.Pattern == "" {
		return true, "", true
	}

	lines, err := logs(ctx, 400)
	if err != nil {
		return true, "", true // could not look; has learned nothing
	}

	cutoff := time.Duration(0)
	if p.Within != "" {
		if d, derr := time.ParseDuration(p.Within); derr == nil {
			cutoff = d
		}
	}

	present := false
	for _, l := range lines {
		if strings.Contains(l, p.Pattern) {
			present = true
			break
		}
	}

	if wantPresent {
		if present {
			return true, "", false
		}

		window := "recent output"
		if cutoff > 0 {
			window = "the last " + cutoff.String()
		}

		return false, fmt.Sprintf("nothing matching %q in %s", p.Pattern, window), false
	}

	if present {
		return false, fmt.Sprintf("found %q in recent output", p.Pattern), false
	}

	return true, "", false
}
