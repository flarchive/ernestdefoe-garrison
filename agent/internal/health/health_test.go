package health

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Captured from `/proc/net/udp` INSIDE the live Valheim container on
// 2026-09-15 — the healthy line verbatim, and the same line with the rx_queue
// field set to the 9600 measured during the outage.
//
// 🚨 Captured rather than hand-written, and the difference mattered: the first
// version of this fixture had the port as 0991 (2449) because the hex was
// typed from memory, and the test failed against a parser that was correct.
// A fixture invented alongside the code it tests agrees with the code's bugs.
const (
	// During the outage: 9600 bytes queued and unread, for twenty hours.
	// 0x2580 == 9600.
	procUDPStuck = `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
34151: 00000000:0999 00000000:0000 07 00000000:00002580 00:00000000 00000000     0        0 221028890 2 0000000000000000 0
`
	// Verbatim from the healthy server: the same socket, drained.
	procUDPHealthy = `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
34151: 00000000:0999 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 221028890 2 0000000000000000 0
`
)

// 0x0999 == 2457, Valheim's query port.
const valheimQueryPort = 2457

// 🚨 The parse, against real captured data rather than a hand-written line.
// A parser that reads the wrong column is the classic way a health check
// becomes confidently wrong, and hex in the middle of a table is exactly where
// that happens.
func TestReadsTheReceiveQueueFromRealProcOutput(t *testing.T) {
	q, found := parseUDPQueue(strings.NewReader(procUDPStuck), valheimQueryPort)
	if !found {
		t.Fatal("did not find the socket for UDP 2457")
	}
	if q != 9600 {
		t.Fatalf("queue = %d, want 9600 — the exact value measured during the outage", q)
	}

	q, found = parseUDPQueue(strings.NewReader(procUDPHealthy), valheimQueryPort)
	if !found {
		t.Fatal("did not find the socket in the healthy capture")
	}
	if q != 0 {
		t.Fatalf("healthy queue = %d, want 0", q)
	}
}

func TestIgnoresOtherPorts(t *testing.T) {
	if _, found := parseUDPQueue(strings.NewReader(procUDPStuck), 9999); found {
		t.Fatal("matched a port that is not in the table")
	}
}

// 🚨 THE TEST THIS WHOLE PACKAGE EXISTS FOR.
//
// The outage: process alive, world saving, lobby refreshing, and unjoinable.
// Liveness says fine. Readiness must not.
func TestTheValheimOutageIsDetected(t *testing.T) {
	// What the log actually contained, seconds after the session registered.
	outageLog := []string{
		"Session \"Shattered Pact\" registered with join code 181140",
		"NullReferenceException: Object reference not set to an instance of an object",
		"  at ZPlayFabMatchmaking.OnCheckJoinCodeSuccess (PlayFab.MultiplayerModels.FindLobbiesResult result)",
		"World save (5/5) done. Total time [22ms]",
		"Connections 0 ZDOS:123229  sent:0 recv:0",
	}

	probes := []Probe{
		{Name: "matchmaking threw and never recovered", Type: "log_match", Pattern: "ZPlayFabMatchmaking"},
	}

	report := Check(context.Background(), true, probes, staticLog(outageLog))

	if report.State != StateUnready {
		t.Fatalf("state = %q, want %q — a running-but-unjoinable server must not read as healthy", report.State, StateUnready)
	}
	if report.Summary == "" {
		t.Fatal("no summary; an operator needs to be told WHICH check failed")
	}
}

// The same probes against the log of a server that is genuinely fine must not
// fire. A probe that cries wolf is switched off within a week, which leaves
// the operator worse off than having no probe at all.
func TestAHealthyServerDoesNotAlert(t *testing.T) {
	healthyLog := []string{
		"Game server connected",
		"Session \"Shattered Pact\" registered with join code 181140",
		"Connections 0 ZDOS:123229  sent:0 recv:0",
		"World save (5/5) done. Total time [19ms]",
	}

	probes := []Probe{
		{Name: "matchmaking threw and never recovered", Type: "log_match", Pattern: "ZPlayFabMatchmaking"},
		{Name: "the session is registered", Type: "log_quiet", Pattern: "registered with join code"},
	}

	report := Check(context.Background(), true, probes, staticLog(healthyLog))

	if report.State != StateOK {
		t.Fatalf("state = %q, want %q. Details: %+v", report.State, StateOK, report.Results)
	}
}

// 🚨 "We did not look" must never render as "we looked and it was fine".
func TestNoProbesIsUnknownNotOK(t *testing.T) {
	report := Check(context.Background(), true, nil, nil)

	if report.State != StateUnknown {
		t.Fatalf("state = %q, want %q — a server with no probes has not been checked", report.State, StateUnknown)
	}
}

func TestAProbeThatCannotRunIsSkippedNotFailed(t *testing.T) {
	// A log probe with no log source has learned nothing. Treating that as a
	// failure would restart healthy servers whose driver cannot tail.
	probes := []Probe{{Name: "crash signature", Type: "log_match", Pattern: "boom"}}

	report := Check(context.Background(), true, probes, nil)

	if report.State != StateUnknown {
		t.Fatalf("state = %q, want %q", report.State, StateUnknown)
	}
	if len(report.Results) != 1 || !report.Results[0].Skipped {
		t.Fatalf("probe should be marked skipped, got %+v", report.Results)
	}
}

// 🚨 A typo in a manifest must not restart a healthy game.
func TestAnUnknownProbeTypeIsSkipped(t *testing.T) {
	probes := []Probe{{Name: "typo", Type: "udp_recvqq", Port: 2457}}

	report := Check(context.Background(), true, probes, nil)

	if report.State == StateUnready {
		t.Fatal("an unrecognised probe type made the server look broken")
	}
	if !report.Results[0].Skipped {
		t.Fatalf("want skipped, got %+v", report.Results[0])
	}
}

// A stopped server reports down, and does NOT produce a page of readiness
// failures that all say the same thing and bury the one fact that matters.
func TestAStoppedServerReportsDownWithoutRunningReadinessProbes(t *testing.T) {
	probes := []Probe{{Name: "crash signature", Type: "log_match", Pattern: "boom"}}

	report := Check(context.Background(), false, probes, staticLog([]string{"boom"}))

	if report.State != StateDown {
		t.Fatalf("state = %q, want %q", report.State, StateDown)
	}
	if len(report.Results) != 0 {
		t.Fatalf("readiness probes ran against a stopped server: %+v", report.Results)
	}
}

// An advisory probe reports but never escalates: real signals that are not
// conclusive on their own must not restart anything.
func TestWarnProbesDoNotMakeAServerUnready(t *testing.T) {
	probes := []Probe{{Name: "advisory", Type: "log_match", Pattern: "boom", Warn: true}}

	report := Check(context.Background(), true, probes, staticLog([]string{"boom happened"}))

	if report.State != StateOK {
		t.Fatalf("state = %q, want %q — a warn probe must not escalate", report.State, StateOK)
	}
	if report.Results[0].OK {
		t.Fatal("the warn probe should still be reported as failing")
	}
}

func staticLog(lines []string) LogSource {
	return func(context.Context, int) ([]string, error) { return lines, nil }
}

// 🚨 Every finding goes out twice: as an ID with params the forum translates,
// and as the English an older forum shows. Losing either half is silent — the
// forum just falls back — so the pairing is checked for every path that
// produces a message.
func TestEveryFindingCarriesAnIDParamsAndTheEnglish(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		probe   Probe
		logs    []string
		id      string
		params  Params
		english string
	}{
		{"crash signature found", Probe{Name: "crash", Type: "log_match", Pattern: "boom"}, []string{"boom"},
			DetailLogFound, Params{"pattern": "boom"}, `found "boom" in recent output`},
		{"sign of life missing", Probe{Name: "alive", Type: "log_quiet", Pattern: "joined"}, []string{"nothing"},
			DetailLogAbsent, Params{"pattern": "joined"}, `nothing matching "joined" in recent output`},
		{"sign of life missing in a window", Probe{Name: "alive", Type: "log_quiet", Pattern: "joined", Within: "10m"}, []string{"nothing"},
			DetailLogAbsentWithin, Params{"pattern": "joined", "within": "10m0s"}, `nothing matching "joined" in the last 10m0s`},
		{"unknown type", Probe{Name: "typo", Type: "udp_recvqq", Port: 1}, nil,
			DetailUnknownType, Params{"type": "udp_recvqq"}, `unknown probe type "udp_recvqq"`},
		{"tcp with no port", Probe{Name: "tcp", Type: "tcp"}, nil,
			DetailNoPort, nil, "no port configured"},
	}

	for _, c := range cases {
		r := run(ctx, c.probe, staticLog(c.logs))

		if r.ID != c.id {
			t.Errorf("%s: id = %q, want %q", c.name, r.ID, c.id)
		}
		if r.Detail != c.english {
			t.Errorf("%s: detail = %q, want the unchanged English %q", c.name, r.Detail, c.english)
		}
		if len(r.Params) != len(c.params) {
			t.Errorf("%s: params = %v, want %v", c.name, r.Params, c.params)
		}
		for k, v := range c.params {
			if r.Params[k] != v {
				t.Errorf("%s: params[%q] = %v, want %v", c.name, k, r.Params[k], v)
			}
		}
	}
}

func TestTCPFindingsCarryThePort(t *testing.T) {
	// Port 1 on loopback: nothing listens there on any sane host.
	ok, m := tcpConnect(context.Background(), 1)
	if ok {
		t.Skip("something is listening on TCP 1")
	}
	if m.id != DetailTCPRefused || m.params["port"] != 1 || m.english != "nothing accepting on TCP 1" {
		t.Fatalf("got %+v", m)
	}
}

func TestEverySummaryCarriesAnID(t *testing.T) {
	ctx := context.Background()
	failing := []Probe{{Name: "matchmaking threw", Type: "log_match", Pattern: "boom"}}
	skipped := []Probe{{Name: "crash", Type: "log_match", Pattern: "boom"}}

	cases := []struct {
		name    string
		report  Report
		id      string
		summary string
	}{
		{"stopped", Check(ctx, false, nil, nil), SummaryNotRunning, "not running"},
		{"no probes", Check(ctx, true, nil, nil), SummaryNoProbes, "no readiness probes configured for this game"},
		{"all skipped", Check(ctx, true, skipped, nil), SummaryNoneEvaluated, "no probe could be evaluated"},
		{"a probe failed", Check(ctx, true, failing, staticLog([]string{"boom"})), SummaryProbeFailed, "matchmaking threw"},
	}

	for _, c := range cases {
		if c.report.SummaryID != c.id || c.report.Summary != c.summary {
			t.Errorf("%s: summary = %q / %q, want %q / %q", c.name, c.report.SummaryID, c.report.Summary, c.id, c.summary)
		}
	}

	// The failing probe's name is the operator's own words, passed through.
	r := cases[3].report
	if r.SummaryParams["probe"] != "matchmaking threw" {
		t.Fatalf("summaryParams = %v, want the probe's name", r.SummaryParams)
	}
}

// The JSON keys are what the PHP side reads. A renamed tag breaks translation
// on every forum without failing anything, so the wire shape is pinned here.
func TestTheWireShapeOfIDsAndParams(t *testing.T) {
	report := Check(context.Background(), true,
		[]Probe{{Name: "crash", Type: "log_match", Pattern: "boom"}},
		staticLog([]string{"boom"}))

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if got["summary"] != "crash" || got["summaryId"] != "probe_failed" {
		t.Fatalf("summary keys wrong: %s", raw)
	}
	if p, _ := got["summaryParams"].(map[string]any); p["probe"] != "crash" {
		t.Fatalf("summaryParams wrong: %s", raw)
	}

	res := got["results"].([]any)[0].(map[string]any)
	if res["id"] != "log_found" || res["detail"] != `found "boom" in recent output` {
		t.Fatalf("result keys wrong: %s", raw)
	}
	if p, _ := res["params"].(map[string]any); p["pattern"] != "boom" {
		t.Fatalf("result params wrong: %s", raw)
	}
}
