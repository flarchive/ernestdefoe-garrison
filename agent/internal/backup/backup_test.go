package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) Config {
	t.Helper()

	root := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "worlds"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worlds", "world.db"), []byte("a world"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server.cfg"), []byte("setting=1"), 0o640); err != nil {
		t.Fatal(err)
	}

	return Config{
		Dir:   filepath.Join(t.TempDir(), "backups"),
		Root:  root,
		Paths: []string{"worlds", "server.cfg"},
		Keep:  3,
	}
}

func TestRoundTrip(t *testing.T) {
	cfg := fixture(t)

	b, err := Create(context.Background(), cfg, "Shattered Pact", false)
	if err != nil {
		t.Fatal(err)
	}
	if b.Size == 0 {
		t.Fatal("wrote an empty backup")
	}

	// Destroy the world, then restore it.
	if err := os.WriteFile(filepath.Join(cfg.Root, "worlds", "world.db"), []byte("ruined"), 0o640); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(context.Background(), cfg, b.ID, false); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(cfg.Root, "worlds", "world.db"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a world" {
		t.Fatalf("world.db is %q, want the restored contents", got)
	}
}

// 🚨 The safety copy is not optional, and this is the test that says so. An
// operator who restores the WRONG backup has, at that moment, destroyed the
// only copy of what they had — unless this ran.
func TestRestoreAlwaysTakesASafetyCopyFirst(t *testing.T) {
	cfg := fixture(t)

	b, err := Create(context.Background(), cfg, "srv", false)
	if err != nil {
		t.Fatal(err)
	}

	// The state that is about to be overwritten.
	if err := os.WriteFile(filepath.Join(cfg.Root, "worlds", "world.db"), []byte("the state about to be lost"), 0o640); err != nil {
		t.Fatal(err)
	}

	safety, err := Restore(context.Background(), cfg, b.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if safety == nil {
		t.Fatal("no safety copy was reported")
	}
	if !safety.Safety {
		t.Fatal("the safety copy is not marked as one")
	}

	// And it must actually contain what was there, not be an empty gesture.
	if err := Delete(cfg, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), cfg, safety.ID, false); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(filepath.Join(cfg.Root, "worlds", "world.db"))
	if string(got) != "the state about to be lost" {
		t.Fatalf("safety copy restored %q — it did not capture the pre-restore state", got)
	}
}

// 🚨 Writing a world file under a live server corrupts it, and the corruption
// shows up hours later looking like a game bug.
func TestRestoreRefusesWhileTheServerIsRunning(t *testing.T) {
	cfg := fixture(t)

	b, _ := Create(context.Background(), cfg, "srv", false)

	if _, err := Restore(context.Background(), cfg, b.ID, true); err == nil {
		t.Fatal("restored into a running server")
	}
}

// 🚨 ZIP-SLIP. A backup file is the one input here that might not have come
// from this agent — somebody copies one between hosts, or restores one a
// friend sent.
func TestARestoreCannotWriteOutsideTheServerDirectory(t *testing.T) {
	cfg := fixture(t)

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(t.TempDir(), "victim.txt")
	if err := os.WriteFile(outside, []byte("untouched"), 0o640); err != nil {
		t.Fatal(err)
	}

	// A hand-built archive with a traversing entry.
	malicious := filepath.Join(cfg.Dir, "evil-20260915-120000-aaaa.tar.gz")
	writeTar(t, malicious, map[string]string{
		"worlds/world.db":                   "fine",
		"../../../../../../../.." + outside: "OWNED",
	})

	_, err := Restore(context.Background(), cfg, "evil-20260915-120000-aaaa.tar.gz", false)
	if err == nil {
		t.Fatal("a traversing archive was restored without complaint")
	}
	if !strings.Contains(err.Error(), "outside the server directory") {
		t.Fatalf("refused, but for the wrong reason: %v", err)
	}

	if got, _ := os.ReadFile(outside); string(got) != "untouched" {
		t.Fatalf("the file outside the tree was overwritten with %q", got)
	}
}

// Every way of naming a file that is not a backup id must be refused, because
// the id is the only thing standing between a request and an arbitrary path.
func TestOnlyGeneratedIdsAreAccepted(t *testing.T) {
	bad := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"srv-20260915-120000-aaaa.tar.gz/../../x",
		"..%2fsrv-20260915-120000-aaaa.tar.gz",
		"srv.tar.gz",
		"",
		"srv-20260915-120000-aaaa.tar.gz.partial",
		// The old format, without the collision suffix — no longer generated,
		// so no longer accepted.
		"srv-20260915-120000.tar.gz",
	}

	for _, id := range bad {
		if ValidID(id) {
			t.Errorf("accepted %q as a backup id", id)
		}
	}

	good := []string{
		"srv-20260915-120000-a1b2.tar.gz",
		"shattered-pact-20260915-235959-zzzz-safety.tar.gz",
	}

	for _, id := range good {
		if !ValidID(id) {
			t.Errorf("refused %q, which is a name this package generates", id)
		}
	}
}

// 🚨 A half-written backup must never appear in the list. Discovering it is
// truncated at the only moment anybody opens one is the worst possible time.
func TestAnInterruptedBackupIsNotListed(t *testing.T) {
	cfg := fixture(t)

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dir, "srv-20260915-120000-aaaa.tar.gz.partial"), []byte("half"), 0o640); err != nil {
		t.Fatal(err)
	}

	list, err := List(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("listed %v — a .partial file was treated as a backup", list)
	}
}

// 🚨 Retention must never delete a safety copy: it exists because somebody was
// about to overwrite something, which is when losing it costs most.
func TestPruneKeepsSafetyCopies(t *testing.T) {
	cfg := fixture(t)
	cfg.Keep = 1

	for i := 0; i < 3; i++ {
		if _, err := Create(context.Background(), cfg, "srv", false); err != nil {
			t.Fatal(err)
		}
		// Distinct second-resolution timestamps.
		touchBack(t, cfg, i)
	}

	if _, err := Create(context.Background(), cfg, "srv", true); err != nil {
		t.Fatal(err)
	}

	if _, err := Prune(cfg); err != nil {
		t.Fatal(err)
	}

	list, _ := List(cfg)

	var ordinary, safety int
	for _, b := range list {
		if b.Safety {
			safety++
		} else {
			ordinary++
		}
	}

	if safety != 1 {
		t.Fatalf("safety copies after prune: %d, want 1", safety)
	}
	if ordinary > cfg.Keep {
		t.Fatalf("kept %d ordinary backups, cap is %d", ordinary, cfg.Keep)
	}
}

func touchBack(t *testing.T, cfg Config, i int) {
	t.Helper()

	list, _ := List(cfg)
	if len(list) == 0 {
		return
	}

	path := filepath.Join(cfg.Dir, list[0].ID)
	when := list[0].At.Add(-time.Duration(i+1) * time.Hour)
	_ = os.Chtimes(path, when, when)
}

func writeTar(t *testing.T, dest string, entries map[string]string) {
	t.Helper()

	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o640,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	tw.Close()
	gz.Close()
}

// writeEntries builds an archive from explicit headers, for the entry types a
// crafted backup would use.
func writeEntries(t *testing.T, dest string, entries []tar.Header, bodies map[string]string) {
	t.Helper()

	f, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
			if h.Mode == 0 {
				h.Mode = 0o640
			}
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	tw.Close()
	gz.Close()
}

func victim(t *testing.T) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(file, []byte("untouched"), 0o640); err != nil {
		t.Fatal(err)
	}
	return dir, file
}

func assertUntouched(t *testing.T, dir, file string) {
	t.Helper()
	if got, _ := os.ReadFile(file); string(got) != "untouched" {
		t.Fatalf("the file outside the tree was overwritten with %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a file was created outside the tree")
	}
}

const crafted = "evil-20260915-120000-aaaa.tar.gz"

// 🚨 An archive that plants a link to outside the tree and then writes a file
// THROUGH it. Every name passes the zip-slip check; the filesystem is what
// sends the write elsewhere.
func TestARestoreDoesNotWriteThroughALinkItCreated(t *testing.T) {
	cfg := fixture(t)
	os.MkdirAll(cfg.Dir, 0o750)
	outDir, outFile := victim(t)

	writeEntries(t, filepath.Join(cfg.Dir, crafted), []tar.Header{
		{Name: "worlds/ssh", Typeflag: tar.TypeSymlink, Linkname: outDir},
		{Name: "worlds/ssh/authorized_keys", Typeflag: tar.TypeReg},
		{Name: "worlds/ssh/pwned", Typeflag: tar.TypeReg},
	}, map[string]string{"worlds/ssh/authorized_keys": "OWNED", "worlds/ssh/pwned": "OWNED"})

	Restore(context.Background(), cfg, crafted, false)

	assertUntouched(t, outDir, outFile)
}

// 🚨 The game itself can plant the link — the directory is writable by
// whatever runs in it — and the agent restores as root.
func TestARestoreDoesNotWriteThroughALinkAlreadyInTheTree(t *testing.T) {
	cfg := fixture(t)
	os.MkdirAll(cfg.Dir, 0o750)
	outDir, outFile := victim(t)

	writeEntries(t, filepath.Join(cfg.Dir, crafted), []tar.Header{
		{Name: "plugins/authorized_keys", Typeflag: tar.TypeReg},
		{Name: "plugins/pwned", Typeflag: tar.TypeReg},
	}, map[string]string{"plugins/authorized_keys": "OWNED", "plugins/pwned": "OWNED"})

	// Planted after the archive was made: a directory that is now a link out.
	if err := os.Symlink(outDir, filepath.Join(cfg.Root, "plugins")); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(context.Background(), cfg, crafted, false); err == nil {
		t.Fatal("a restore through a planted directory link reported success")
	}

	assertUntouched(t, outDir, outFile)
}

// A link planted where a FILE is about to be restored is replaced, not
// truncated through.
func TestARestoreReplacesALinkInAFilesPlace(t *testing.T) {
	cfg := fixture(t)
	_, outFile := victim(t)

	b, err := Create(context.Background(), cfg, "srv", false)
	if err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(cfg.Root, "server.cfg")
	os.Remove(cfgPath)
	if err := os.Symlink(outFile, cfgPath); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(context.Background(), cfg, b.ID, false); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(outFile); string(got) != "untouched" {
		t.Fatalf("the restore wrote through the link: outside file is %q", got)
	}

	info, err := os.Lstat(cfgPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("server.cfg is still a link after the restore")
	}
	if got, _ := os.ReadFile(cfgPath); string(got) != "setting=1" {
		t.Fatalf("server.cfg is %q, want the restored contents", got)
	}
}

func TestARestoreRefusesHardLinks(t *testing.T) {
	cfg := fixture(t)
	os.MkdirAll(cfg.Dir, 0o750)

	writeEntries(t, filepath.Join(cfg.Dir, crafted), []tar.Header{
		{Name: "worlds/world.db", Typeflag: tar.TypeLink, Linkname: "/etc/passwd"},
	}, nil)

	if _, err := Restore(context.Background(), cfg, crafted, false); err == nil {
		t.Fatal("an archive with a hard link was restored")
	}

	if got, _ := os.ReadFile(filepath.Join(cfg.Root, "worlds", "world.db")); string(got) != "a world" {
		t.Fatalf("a refused archive still changed the tree: world.db is %q", got)
	}
}

// The legitimate half: a link inside the tree survives a round trip.
func TestALinkInsideTheTreeRoundTrips(t *testing.T) {
	cfg := fixture(t)

	if err := os.Symlink("world.db", filepath.Join(cfg.Root, "worlds", "latest")); err != nil {
		t.Fatal(err)
	}

	b, err := Create(context.Background(), cfg, "srv", false)
	if err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(cfg.Root, "worlds", "latest"))

	if _, err := Restore(context.Background(), cfg, b.ID, false); err != nil {
		t.Fatal(err)
	}

	target, err := os.Readlink(filepath.Join(cfg.Root, "worlds", "latest"))
	if err != nil || target != "world.db" {
		t.Fatalf("the in-tree link came back as %q (%v)", target, err)
	}
}
