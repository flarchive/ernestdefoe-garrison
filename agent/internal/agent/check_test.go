package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/offsite"
	"github.com/ernestdefoe/garrison/internal/players"
	"github.com/ernestdefoe/garrison/internal/settings"
)

/*
🚨 A short context, so the suite stays fast.

Check bounds its own network calls at thirty seconds, which is right for an
operator running --check against a real bucket and wrong for a test suite: the
unreachable-endpoint case spent ten seconds waiting for a TCP timeout. Passing a
deadline in takes the shorter of the two, so the same code path runs and the
suite finishes in milliseconds.
*/
func findings(t *testing.T, servers []driver.Server, available ...string) []Finding {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	t.Cleanup(cancel)

	return Check(ctx, "", servers, available, nil)
}

func bad(fs []Finding) []string {
	var out []string

	for _, f := range fs {
		if f.Bad {
			out = append(out, f.Text)
		}
	}

	return out
}

func mentions(fs []Finding, substring string) bool {
	for _, f := range fs {
		if strings.Contains(f.Text, substring) {
			return true
		}
	}

	return false
}

func TestACleanConfigurationHasNoProblems(t *testing.T) {
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "worlds"), 0o750); err != nil {
		t.Fatal(err)
	}

	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		BackupRoot: root, BackupPaths: []string{"worlds"},
	}}, "process")

	if problems := bad(got); len(problems) != 0 {
		t.Fatalf("a clean configuration reported problems: %v", problems)
	}
}

/*
🚨 THE ONE THIS FILE EXISTS FOR.

A backup silently missing one of three directories is the worst shape this bug
takes: the archive is created, it is listed, it restores without error, and the
thing somebody needed is not in it. They find out while restoring from it.
*/
func TestEveryBackupPathIsChecked(t *testing.T) {
	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "worlds"), 0o750); err != nil {
		t.Fatal(err)
	}

	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		BackupRoot:  root,
		BackupPaths: []string{"worlds", "config", "mods"},
	}}, "process")

	problems := bad(got)

	if len(problems) != 2 {
		t.Fatalf("reported %d problems, want one per missing path: %v", len(problems), problems)
	}

	for _, want := range []string{"config", "mods"} {
		if !mentions(got, want) {
			t.Errorf("the missing path %q was not reported", want)
		}
	}
}

func TestHalfConfiguredBackupsAreReported(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process", BackupRoot: "/srv/game",
	}}, "process")

	if !mentions(got, "half configured") {
		t.Fatalf("a backupRoot with no paths was not reported: %+v", got)
	}
}

func TestAMissingDriverIsAProblem(t *testing.T) {
	got := findings(t, []driver.Server{{ID: "srv", Driver: "docker"}}, "process")

	if len(bad(got)) == 0 {
		t.Fatalf("a server on an unavailable driver passed: %+v", got)
	}
}

/*
🚨 A `keys` entry that matches nothing is almost always a typo, and it fails in
the confusing direction: the setting is simply not offered, which reads as
Garrison not supporting it rather than as a mistake in the config.
*/
func TestAnAllowedKeyThatIsNotInTheFileIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.properties")

	if err := os.WriteFile(path, []byte("motd=hello\nmax-players=20\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Config: []settings.File{{
			ID: "props", Path: path, Format: settings.FormatProperties,
			Keys: []string{"motd", "mtod"},
		}},
	}}, "process")

	if !mentions(got, "mtod") {
		t.Fatalf("the typo'd key was not reported: %+v", got)
	}
}

// 🚨 A duplicate id means Find() returns the first and the second is
// unreachable — an editable file that silently is not.
func TestDuplicateConfigIdsAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.properties")

	if err := os.WriteFile(path, []byte("x=1\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Config: []settings.File{
			{ID: "props", Path: path, Format: settings.FormatProperties},
			{ID: "props", Path: path, Format: settings.FormatProperties},
		},
	}}, "process")

	if !mentions(got, "share the id") {
		t.Fatalf("duplicate config ids were not reported: %+v", got)
	}
}

func TestAMissingConfigFileIsReported(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Config: []settings.File{{ID: "props", Path: "/nowhere/server.properties", Format: settings.FormatProperties}},
	}}, "process")

	if !mentions(got, "cannot be read") {
		t.Fatalf("a missing config file was not reported: %+v", got)
	}
}

func TestAnUnknownConfigFormatIsReported(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Config: []settings.File{{ID: "props", Path: "/tmp/x", Format: "yaml"}},
	}}, "process")

	if !mentions(got, "known formats") {
		t.Fatalf("an unknown format was not reported: %+v", got)
	}
}

func TestABadPlayerPatternIsReported(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Players: players.Config{Join: `(?P<name>[`, Leave: `x`},
	}}, "process")

	if len(bad(got)) == 0 {
		t.Fatalf("an invalid player pattern passed: %+v", got)
	}
}

/*
🚨 Information, not a failure. Plenty of games have no whisper command —
Valheim among them — and reading who is playing is useful on its own. What
would be wrong is letting an operator believe the link flow works when the
forum will never offer it there.
*/
func TestAServerThatCannotWhisperSaysSoWithoutFailing(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Players: players.Config{Preset: "valheim"},
	}}, "process")

	if len(bad(got)) != 0 {
		t.Fatalf("a server with no whisper command was treated as broken: %v", bad(got))
	}

	if !mentions(got, "cannot be linked") {
		t.Fatalf("the absence of a whisper command was not mentioned: %+v", got)
	}
}

/*
🚨 A credentials check that only looks for non-empty strings passes for a typo'd
secret key — and then off-site backups fail every night into a log nobody reads,
while the operator believes their worlds are safe somewhere else.

An unreachable endpoint is the cheapest version of that to test, and it goes
through exactly the code path a wrong key would.
*/
func TestUnreachableOffsiteStorageIsAProblem(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Offsite: offsite.Config{
			// Reserved for documentation; guaranteed not to answer.
			Endpoint:  "https://192.0.2.1",
			Region:    "us-east-1",
			Bucket:    "backups",
			AccessKey: "k",
			SecretKey: "s",
			PathStyle: true,
		},
	}}, "process")

	if !mentions(got, "could not be listed") {
		t.Fatalf("an unreachable bucket passed the check: %+v", got)
	}
}

func TestAnHTTPOffsiteEndpointIsAProblem(t *testing.T) {
	got := findings(t, []driver.Server{{
		ID: "srv", Driver: "process",
		Offsite: offsite.Config{
			Endpoint: "http://s3.example.com", Region: "us-east-1",
			Bucket: "b", AccessKey: "k", SecretKey: "s",
		},
	}}, "process")

	if !mentions(got, "https") {
		t.Fatalf("a plain-http endpoint was not reported: %+v", got)
	}
}

// Nothing configured is not a fault. Most servers have no off-site storage, no
// editable config and no player tracking, and a check that nags about every
// optional feature is one people stop reading.
func TestOptionalFeaturesAreNotNagged(t *testing.T) {
	got := findings(t, []driver.Server{{ID: "srv", Driver: "process"}}, "process")

	if problems := bad(got); len(problems) != 0 {
		t.Fatalf("a minimal server reported problems: %v", problems)
	}
}

/*
🚨 THAT FILE HOLDS THE AGENT'S TOKEN.

The token is the whole of this agent's authority — anything that can read it can
impersonate the host to the forum — and the operator's off-site bucket keys are
usually in the same file. A world-readable config is not something anybody
notices: it is the default umask on most distributions, and a working agent
looks identical either way. Found on the dev host at 0644.
*/
func TestAWorldReadableConfigIsAProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")

	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Check(context.Background(), path, nil, []string{"process"}, nil)

	if len(bad(got)) == 0 {
		t.Fatalf("a 0644 config passed: %+v", got)
	}

	if !mentions(got, "token") {
		t.Fatalf("the reason was not given: %+v", got)
	}
}

func TestATightConfigIsFine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.json")

	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Check(context.Background(), path, nil, []string{"process"}, nil)

	if problems := bad(got); len(problems) != 0 {
		t.Fatalf("a 0600 config reported problems: %v", problems)
	}
}
