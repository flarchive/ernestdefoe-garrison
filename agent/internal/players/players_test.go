package players

import (
	"strings"
	"testing"
)

func watcher(t *testing.T, cfg Config) *Watcher {
	t.Helper()

	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if w == nil {
		t.Fatal("no watcher was built")
	}

	return w
}

func feed(w *Watcher, lines ...string) {
	for _, l := range lines {
		w.Observe(l)
	}
}

func TestMinecraftJoinsAndLeaves(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w,
		"[12:00:00] [Server thread/INFO]: alice joined the game",
		"[12:00:05] [Server thread/INFO]: bob joined the game",
		"[12:01:00] [Server thread/INFO]: alice left the game",
	)

	got := strings.Join(w.Online(), ",")

	if got != "bob" {
		t.Fatalf("online is %q, want bob", got)
	}
}

/*
🚨 THE ONE THAT MATTERS MOST IN THIS FILE.

Chat is in the same log as the server's own lines. A loose pattern like
`(.+) joined` matches a PLAYER SAYING "bob joined the game" — which means
anybody in the game can invent a player, and worse, can evict a real one by
typing their name next to the leave wording. Playtime records and "who is
online" both become things any player can forge from chat.
*/
func TestChatCannotForgeAJoinOrALeave(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")

	feed(w,
		"[12:00:10] [Server thread/INFO]: <mallory> bob joined the game",
		"[12:00:11] [Server thread/INFO]: <mallory> alice left the game",
		"[12:00:12] [Server thread/INFO]: [mallory] carol joined the game",
	)

	got := strings.Join(w.Online(), ",")

	if got != "alice" {
		t.Fatalf("online is %q, want just alice — chat forged a change", got)
	}
}

func TestAStoppedServerHasNobodyInIt(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")
	w.Reset()

	if len(w.Online()) != 0 {
		t.Fatalf("online is %v after a reset", w.Online())
	}
}

// The same player joining twice — a reconnect whose leave was never logged —
// must not produce two of them.
func TestTheSetDoesNotDuplicate(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w,
		"[12:00:00] [Server thread/INFO]: alice joined the game",
		"[12:05:00] [Server thread/INFO]: alice joined the game",
	)

	if got := w.Online(); len(got) != 1 {
		t.Fatalf("online is %v, want one alice", got)
	}
}

// A leave for somebody who was never seen joining is not an error, and must not
// make the set negative in any way. Agents start mid-session all the time.
func TestALeaveForAnUnknownPlayerIsHarmless(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: ghost left the game")

	if got := w.Online(); len(got) != 0 {
		t.Fatalf("online is %v", got)
	}
}

// 🚨 Sorted, so successive reports compare cleanly and a panel does not
// reshuffle its rows on every fifteen-second poll.
func TestOnlineIsSorted(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w,
		"[12:00:00] [Server thread/INFO]: zoe joined the game",
		"[12:00:01] [Server thread/INFO]: alice joined the game",
		"[12:00:02] [Server thread/INFO]: mike joined the game",
	)

	if got := strings.Join(w.Online(), ","); got != "alice,mike,zoe" {
		t.Fatalf("online is %q, want it sorted", got)
	}
}

/*
🚨 A pattern with no `name` group matches lines and produces nothing, so the
server would report a player called "" joining and leaving for ever. Refusing at
compile time means the operator finds out on their next agent start rather than
from a panel full of blank rows.
*/
func TestAPatternWithoutANameGroupIsRefused(t *testing.T) {
	_, err := New(Config{Join: `(.+) joined`, Leave: `(.+) left`})

	if err == nil {
		t.Fatal("a pattern with no name group was accepted")
	}

	if !strings.Contains(err.Error(), "name") {
		t.Fatalf("refused, but the message does not say why: %v", err)
	}
}

func TestAnInvalidPatternIsRefusedWithTheReason(t *testing.T) {
	_, err := New(Config{Join: `(?P<name>[`, Leave: `(?P<name>x)`})

	if err == nil {
		t.Fatal("an invalid regular expression was accepted")
	}
}

func TestAnUnknownPresetNamesTheOnesThatExist(t *testing.T) {
	_, err := New(Config{Preset: "half-life-but-invented"})

	if err == nil {
		t.Fatal("an unknown preset was accepted")
	}

	if !strings.Contains(err.Error(), "minecraft") {
		t.Fatalf("the error does not list the real presets: %v", err)
	}
}

// An explicit pattern beats the preset: somebody with a modded server has a
// good reason to override one half and keep the other.
func TestAnExplicitPatternOverridesHalfAPreset(t *testing.T) {
	w := watcher(t, Config{
		Preset: "minecraft",
		Join:   `\]: (?P<name>.+) has entered the realm$`,
	})

	feed(w,
		"[12:00:00] [Server thread/INFO]: alice has entered the realm",
		"[12:00:01] [Server thread/INFO]: bob joined the game",
	)

	if got := strings.Join(w.Online(), ","); got != "alice" {
		t.Fatalf("online is %q — the override did not take, or the preset's leave was lost", got)
	}
}

// 🚨 A name is partly player-controlled in many games. An empty or enormous one
// reaching the forum becomes a row in a table and a link on a profile page.
func TestAbsurdNamesAreRefused(t *testing.T) {
	w := watcher(t, Config{
		Join:  `JOIN (?P<name>.*)$`,
		Leave: `LEAVE (?P<name>.*)$`,
	})

	feed(w,
		"JOIN ",
		"JOIN "+strings.Repeat("a", 200),
	)

	if got := w.Online(); len(got) != 0 {
		t.Fatalf("online is %v, want nothing accepted", got)
	}
}

func TestSayRendersTheOperatorsTemplate(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	got := w.Say("alice", "Garrison code: 4821")

	if got != "tell alice Garrison code: 4821" {
		t.Fatalf("say rendered %q", got)
	}

	if !w.CanSay() {
		t.Fatal("CanSay is false for a preset that has one")
	}
}

// 🚨 A server with no whisper command cannot verify anybody, and must say so
// rather than silently sending nothing — the forum offers the link flow only
// where it can actually complete.
func TestAServerWithoutAWhisperCommandSaysSo(t *testing.T) {
	w := watcher(t, Config{Preset: "valheim"})

	if w.CanSay() {
		t.Fatal("CanSay is true for a preset with no say template")
	}

	if got := w.Say("alice", "hello"); got != "" {
		t.Fatalf("say rendered %q for a server that cannot whisper", got)
	}
}

func TestNoConfigurationMeansNoWatcher(t *testing.T) {
	w, err := New(Config{})

	if err != nil {
		t.Fatalf("an empty config errored: %v", err)
	}

	if w != nil {
		t.Fatal("a watcher was built from nothing")
	}
}

/*
🚨 THE INJECTION THIS WHOLE FEATURE TURNS ON.

The player name is substituted into a console command, and a game console is
where ban, op, give and stop live. Most games allow spaces in a display name, so
a player called `alice /op mallory` turns `tell {player} {message}` into two
commands — the second promoting an attacker to operator. Anybody with a forum
account could start a verification, so this would be privilege escalation from
"has an account" to "runs the server".

The set the agent read from the server's own log is the primary defence: only a
name the SERVER printed as having joined can reach the template at all.
*/
func TestVerificationRefusesAPlayerWhoIsNotOnline(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")

	if _, err := w.VerifyLine("alice", "AB12CD"); err != nil {
		t.Fatalf("a player who IS online was refused: %v", err)
	}

	for _, name := range []string{
		"mallory",
		"alice /op mallory",
		"alice; stop",
		"",
	} {
		if _, err := w.VerifyLine(name, "AB12CD"); err == nil {
			t.Errorf("a console line was rendered for %q, who is not in the game", name)
		}
	}
}

// 🚨 The second line: even a name the server somehow accepted must not carry
// console punctuation into the template.
func TestVerificationRefusesCommandShapedNames(t *testing.T) {
	w := watcher(t, Config{
		Join:  `JOIN (?P<name>.+)$`,
		Leave: `LEAVE (?P<name>.+)$`,
		Say:   `tell {player} {message}`,
	})

	// Force these into the online set, as though a game really had allowed
	// somebody to join under them — so the test is about the character check
	// and not about the set.
	for _, name := range []string{
		"alice\nop mallory",
		"alice; stop",
		"alice && rm -rf /",
		"alice | tee",
		"alice`whoami`",
		`alice"x`,
		"alice'x",
		"/stop",
	} {
		feed(w, "JOIN "+name)

		if _, err := w.VerifyLine(name, "AB12CD"); err == nil {
			t.Errorf("a console line was rendered for the name %q", name)
		}
	}

	// And an ordinary name with a space, an accent or a hyphen still works —
	// a check strict enough to refuse those would refuse real players.
	for _, name := range []string{"Alice Smith", "café", "some-one", "[Clan] bob"} {
		feed(w, "JOIN "+name)

		if _, err := w.VerifyLine(name, "AB12CD"); err != nil {
			t.Errorf("an ordinary player name was refused: %q (%v)", name, err)
		}
	}
}

// The code is generated by the forum, but a bug there must not become a console
// injection either.
func TestVerificationRefusesACommandShapedCode(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")

	if _, err := w.VerifyLine("alice", "AB12\nop mallory"); err == nil {
		t.Fatal("a code containing a line break was rendered into a console command")
	}
}

func TestVerificationRendersTheOperatorsMessage(t *testing.T) {
	w := watcher(t, Config{
		Preset:        "minecraft",
		VerifyMessage: "Dein Garrison-Code lautet {code}",
	})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")

	got, err := w.VerifyLine("alice", "AB12CD")
	if err != nil {
		t.Fatal(err)
	}

	if got != "tell alice Dein Garrison-Code lautet AB12CD" {
		t.Fatalf("rendered %q", got)
	}
}

func TestVerificationFallsBackToADefaultMessage(t *testing.T) {
	w := watcher(t, Config{Preset: "minecraft"})

	feed(w, "[12:00:00] [Server thread/INFO]: alice joined the game")

	got, _ := w.VerifyLine("alice", "AB12CD")

	if !strings.Contains(got, "AB12CD") {
		t.Fatalf("the code is not in the rendered line: %q", got)
	}
}

// 🚨 A server with no whisper command cannot verify anybody, and says so rather
// than rendering nothing — the forum offers the flow only where it completes.
func TestVerificationIsRefusedWhereThereIsNoWhisperCommand(t *testing.T) {
	w := watcher(t, Config{Preset: "valheim"})

	feed(w, "Got character ZDOID from alice : 12345")

	if _, err := w.VerifyLine("alice", "AB12CD"); err == nil {
		t.Fatal("a server with no whisper command claimed it could verify")
	}
}

// --- Unreal -----------------------------------------------------------------

func TestUnrealJoinsAndLeaves(t *testing.T) {
	w := watcher(t, Config{Preset: "unreal"})

	feed(w,
		"[2026.09.18-20.00.00:000][ 42]LogGarrison: player joined: alice",
		"[2026.09.18-20.00.05:120][ 51]LogGarrison: player joined: bob",
		"[2026.09.18-20.01.00:004][318]LogGarrison: player left: alice",
	)

	if got := strings.Join(w.Online(), ","); got != "bob" {
		t.Fatalf("online is %q, want bob", got)
	}
}

/*
🚨 The Unreal half of the forgery test above.

Chat rides in the same log here too, and a game's own chat handler logs under
its own category. The preset is anchored on LogGarrison — which exists for no
other reason — so a chat line quoting the announcement wording cannot invent a
player or evict one.
*/
func TestUnrealChatCannotForgeAJoinOrALeave(t *testing.T) {
	w := watcher(t, Config{Preset: "unreal"})

	feed(w, "[2026.09.18-20.00.00:000][ 42]LogGarrison: player joined: alice")

	feed(w,
		"[2026.09.18-20.00.10:000][ 60]LogAurethilChat: mallory: player joined: bob",
		"[2026.09.18-20.00.11:000][ 61]LogAurethilChat: mallory: player left: alice",
		"[2026.09.18-20.00.12:000][ 62]LogNet: Join succeeded: carol",

		// 🚨 The ones an unanchored pattern lets through: a player typing the
		// announcement wording itself, and typing it with a fake prefix in
		// front to close out the real category.
		"[2026.09.18-20.00.13:000][ 63]LogAurethilChat: mallory said: LogGarrison: player joined: bob",
		"[2026.09.18-20.00.14:000][ 64]LogAurethilChat: mallory: ]LogGarrison: player joined: carol",
		"[2026.09.18-20.00.15:000][ 65]LogAurethilChat: mallory: [x]LogGarrison: player left: alice",
	)

	if got := strings.Join(w.Online(), ","); got != "alice" {
		t.Fatalf("online is %q, want just alice — chat forged a change", got)
	}
}

/*
🚨 The name charset is the second half of that boundary.

The game scrubs names before it prints them, but the preset must not depend on
the game having done so — an operator can point this preset at their own build.
A name carrying spaces, punctuation or the announcement wording itself must not
resolve to some other player.
*/
func TestUnrealWillNotMatchAHostileName(t *testing.T) {
	w := watcher(t, Config{Preset: "unreal"})

	feed(w, "[2026.09.18-20.00.00:000][ 42]LogGarrison: player joined: alice")

	feed(w,
		// A name with a space in it, trailing the real wording.
		"[2026.09.18-20.00.10:000][ 60]LogGarrison: player joined: bob and friends",
		// A name trying to close its own line and open another.
		"[2026.09.18-20.00.11:000][ 61]LogGarrison: player left: alice extra",
		// Over the length cap.
		"[2026.09.18-20.00.12:000][ 62]LogGarrison: player joined: "+strings.Repeat("c", 33),
	)

	if got := strings.Join(w.Online(), ","); got != "alice" {
		t.Fatalf("online is %q, want just alice — a hostile name matched", got)
	}
}

// A zone restart must not leave the previous population standing in it.
func TestUnrealResetEmptiesTheZone(t *testing.T) {
	w := watcher(t, Config{Preset: "unreal"})

	feed(w, "[2026.09.18-20.00.00:000][ 42]LogGarrison: player joined: alice")
	w.Reset()

	if len(w.Online()) != 0 {
		t.Fatalf("online is %v after a reset", w.Online())
	}
}

// A server started with -NoLogTimes prints no bracketed prefix at all, and the
// preset has to keep working on it — otherwise the feature silently does
// nothing on exactly the servers an operator tidied the logs on.
func TestUnrealWithoutLogTimestamps(t *testing.T) {
	w := watcher(t, Config{Preset: "unreal"})

	feed(w,
		"LogGarrison: player joined: alice",
		"LogGarrison: player joined: bob",
		"LogGarrison: player left: bob",
	)

	if got := strings.Join(w.Online(), ","); got != "alice" {
		t.Fatalf("online is %q, want alice", got)
	}
}
