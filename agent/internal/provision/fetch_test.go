package provision

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
🚨 The unpacker is the only part of provisioning that handles attacker-supplied
STRUCTURE rather than attacker-supplied bytes.

A download can be pinned, size-capped and served over HTTPS and still contain an
archive entry named `../../etc/cron.d/x`. "It came from the official site" is
not a check — the archive is data, and the guard has to be in the code that
turns an entry name into a path.
*/

func zipWith(t *testing.T, names ...string) string {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for _, n := range names {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func tarGzWith(t *testing.T, names ...string) string {
	t.Helper()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	tw.Close()
	gz.Close()

	path := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestZipCannotEscapeTheInstallDirectory(t *testing.T) {
	for _, name := range []string{"../escaped", "../../etc/cron.d/x", "/etc/passwd", "a/../../b"} {
		dir := t.TempDir()

		err := unpack(zipWith(t, name), "zip", dir)
		if err == nil {
			t.Errorf("entry %q was accepted", name)
		}

		// And nothing was written outside, even partially.
		if _, serr := os.Stat(filepath.Join(filepath.Dir(dir), "escaped")); serr == nil {
			t.Errorf("entry %q wrote outside the directory", name)
		}
	}
}

func TestTarCannotEscapeTheInstallDirectory(t *testing.T) {
	for _, name := range []string{"../escaped", "/etc/passwd", "nested/../../out"} {
		dir := t.TempDir()

		if err := unpack(tarGzWith(t, name), "tar.gz", dir); err == nil {
			t.Errorf("entry %q was accepted", name)
		}
	}
}

func TestOrdinaryArchivesStillUnpack(t *testing.T) {
	dir := t.TempDir()

	if err := unpack(zipWith(t, "server/start.sh", "server/data/world.db"), "zip", dir); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"server/start.sh", "server/data/world.db"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s missing after unpack: %v", want, err)
		}
	}
}

/*
🚨 Plain http is refused. A download that can be intercepted is an install that
can be replaced, and this one ends as a process running on the operator's host.
*/
func TestOnlyHTTPSIsFetched(t *testing.T) {
	_, err := open(context.Background(), "http://example.test/server.jar")

	if err == nil {
		t.Fatal("http was accepted")
	}

	if !strings.Contains(err.Error(), "https") {
		t.Errorf("the refusal should say why: %v", err)
	}
}

func TestATokenResolvesToSomethingFetchable(t *testing.T) {
	// factorio:stable is a fixed alias, so it resolves without a network call.
	got, err := resolveDownload(context.Background(), "factorio:stable")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(got, "https://") {
		t.Errorf("resolved to %q", got)
	}
}

func TestAPlainURLPassesThroughUnchanged(t *testing.T) {
	const u = "https://example.test/server.jar"

	got, err := resolveDownload(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}

	if got != u {
		t.Errorf("got %q, want %q", got, u)
	}
}

func TestUnknownArchiveKindIsRefused(t *testing.T) {
	if err := unpack(zipWith(t, "a"), "rar", t.TempDir()); err == nil {
		t.Fatal("an unknown archive kind should be refused, not guessed at")
	}
}
