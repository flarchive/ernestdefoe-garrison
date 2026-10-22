package driver

import (
	"strings"
	"testing"
	"time"
)

/*
🚨 THE ONE THAT MATTERS MOST IN THIS FILE.

A unit name becomes an argument to systemctl. Today it comes from a config file
the operator wrote, which is why this is not yet a vulnerability — but "the
input is trusted" is exactly the assumption that quietly stops being true: a
provisioning template, an import, an admin field added later. The validator has
to be the thing that holds, not the provenance.

So anything outside a plausible unit name is REFUSED rather than escaped.
Escaping is a thing you can get subtly wrong; a character class is not.
*/
func TestUnitNameRefusesAnythingStrange(t *testing.T) {
	sd := NewSystemd()

	hostile := []string{
		"a;systemctl stop sshd",
		"a && rm -rf /",
		"a`id`",
		"a$(id)",
		"a|tee /etc/passwd",
		"a b",
		"../../etc/passwd",
		"a\nb",
		"a'b",
		`a"b`,
		"a>b",
		"a*",
		strings.Repeat("a", 129),
	}

	for _, name := range hostile {
		if _, err := sd.unit(Server{ID: "s", Unit: name}); err == nil {
			t.Errorf("accepted a unit name it should have refused: %q", name)
		}
	}
}

func TestUnitNameAcceptsRealOnes(t *testing.T) {
	sd := NewSystemd()

	cases := map[string]string{
		// A bare name is a .service, so the journal and the status call agree
		// about which unit they are talking about.
		"aurethil-zone":         "aurethil-zone.service",
		"aurethil-zone.service": "aurethil-zone.service",
		// Templated units are ordinary, and a game panel will meet them.
		"aurethil@saelvarin.service": "aurethil@saelvarin.service",
		"minecraft_survival.service": "minecraft_survival.service",
		"valheim.socket":             "valheim.socket",
	}

	for in, want := range cases {
		got, err := sd.unit(Server{ID: "s", Unit: in})
		if err != nil {
			t.Errorf("refused a real unit name %q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("unit(%q) = %q, want %q", in, got, want)
		}
	}
}

// A server configured for this driver with no unit is a missing line in a file,
// and saying so beats starting nothing and reporting nothing.
func TestUnitNameRequiresOne(t *testing.T) {
	sd := NewSystemd()
	for _, blank := range []string{"", "   ", "\t"} {
		if _, err := sd.unit(Server{ID: "s", Unit: blank}); err == nil {
			t.Errorf("accepted a blank unit name %q", blank)
		}
	}
}

/*
🚨 The console must refuse honestly.

A systemd service has no stdin to write to. Returning success and doing nothing
would make every console command appear to work — and in-game verification,
which delivers its code through the console, would fail in a way nobody could
explain.
*/
func TestSendIsRefusedRatherThanIgnored(t *testing.T) {
	sd := NewSystemd()
	err := sd.Send(t.Context(), Server{ID: "s", Unit: "x.service"}, "say hello")
	if err == nil {
		t.Fatal("Send claimed to have written to a unit that has no stdin")
	}
	if !strings.Contains(err.Error(), "no stdin") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}
}

// journalctl prefixes every line with a timestamp, a host and the unit. The
// forum wants what the game said and when it said it, not that envelope.
func TestJournalLinesKeepTheirTimeAndLoseThePrefix(t *testing.T) {
	line := parseJournalLine("zone",
		"2026-09-19T00:31:14+0000 server.convoro.co AurethilServer[1484316]: LogAurethilNet: Zone ready on OpenWorld")

	if line.Text != "LogAurethilNet: Zone ready on OpenWorld" {
		t.Errorf("text is %q, want the game's line without the prefix", line.Text)
	}

	want := time.Date(2026, 9, 19, 0, 31, 14, 0, time.UTC)
	if !line.At.Equal(want) {
		t.Errorf("time is %v, want %v — a console showing when the agent read a line is wrong by however long it was disconnected", line.At, want)
	}
	if line.Server != "zone" {
		t.Errorf("server is %q, want zone", line.Server)
	}
}

// 🚨 A game line containing ": " must not be truncated. The prefix always comes
// first, so only the first separator may be consumed.
func TestJournalLineKeepsColonsInTheGamesOwnText(t *testing.T) {
	line := parseJournalLine("zone",
		"2026-09-19T00:31:14+0000 host AurethilServer[1]: LogGarrison: player joined: alice")

	if line.Text != "LogGarrison: player joined: alice" {
		t.Errorf("text is %q — the game's own colons were eaten", line.Text)
	}
}

// Anything that is not a journal line at all still reaches the console, rather
// than being dropped because it did not parse.
func TestUnparseableJournalLinesAreStillDelivered(t *testing.T) {
	raw := "-- No entries --"
	line := parseJournalLine("zone", raw)
	if line.Text != raw {
		t.Errorf("text is %q, want the line delivered unchanged", line.Text)
	}
	if line.At.IsZero() {
		t.Error("a line with no parseable time should still carry one")
	}
}
