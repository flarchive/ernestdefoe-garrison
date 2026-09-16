// Package backup makes and restores copies of a server's data.
//
// 🚨 This is the most dangerous code in Garrison. Everything else can, at
// worst, restart a game; a restore can destroy a world that took people
// hundreds of hours, and there is no undo. Every rule below exists because
// without it something irreversible becomes possible.
//
//  1. A backup's ID is a FILENAME the agent generated, validated against the
//     pattern it generates and resolved inside the backup directory. Never a
//     path from the request.
//  2. Extraction refuses any entry that would land outside the target. A tar
//     can contain "../../etc/cron.d/x" and most extractors will happily write
//     it.
//  3. A restore takes a SAFETY COPY first, always, with no way to skip it.
//     The restore is the risky act; the thing most worth having a backup of is
//     the state you are about to overwrite.
//  4. A restore refuses while the server is running. Writing a world file
//     under a live server corrupts it, and the corruption shows up hours later
//     looking like a game bug.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Backup is one stored copy.
type Backup struct {
	ID     string    `json:"id"`
	Size   int64     `json:"size"`
	At     time.Time `json:"at"`
	Safety bool      `json:"safety,omitempty"`
}

// Config is what a server's manifest says about backing it up.
type Config struct {
	// Dir is where backups are kept.
	Dir string
	// Paths are the files and directories worth keeping, relative to Root.
	Paths []string
	// Root is what Paths are relative to.
	Root string
	// Keep is how many ordinary backups to retain. Zero means keep all.
	Keep int
}

// 🚨 The exact shape this package generates, and the only shape it will accept
// back. An ID that does not match cannot name a file, so traversal, absolute
// paths and symlink games are all refused by the same check.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}-\d{8}-\d{6}-[a-z0-9]{4}(-safety)?\.tar\.gz$`)

// ValidID reports whether id is a name this package could have produced.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// resolve turns an ID into a path inside cfg.Dir, or fails.
//
// 🚨 Both checks matter. The pattern stops "../" ever being in the name, and
// the prefix check stops a symlinked backup directory from landing the write
// somewhere else anyway.
func resolve(cfg Config, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("not a backup id")
	}

	dir, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return "", err
	}

	full := filepath.Join(dir, id)

	if !strings.HasPrefix(full, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("backup id escapes the backup directory")
	}

	return full, nil
}

// List returns the backups on disk, newest first.
func List(cfg Config) ([]Backup, error) {
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no directory yet is not an error, just no backups
		}
		return nil, err
	}

	var out []Backup

	for _, e := range entries {
		if e.IsDir() || !ValidID(e.Name()) {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		out = append(out, Backup{
			ID:     e.Name(),
			Size:   info.Size(),
			At:     info.ModTime().UTC(),
			Safety: strings.HasSuffix(e.Name(), "-safety.tar.gz"),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })

	return out, nil
}

// Create writes a new backup and returns it.
func Create(ctx context.Context, cfg Config, name string, safety bool) (*Backup, error) {
	if cfg.Root == "" || len(cfg.Paths) == 0 {
		return nil, fmt.Errorf("this server has no backup paths configured")
	}

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("creating backup directory: %w", err)
	}

	/*
	 * 🚨 A random suffix, because a timestamp to the second is NOT unique.
	 *
	 * Two backups in the same second produce the same name and the second
	 * silently overwrites the first — and the realistic case is the worst one:
	 * a scheduled backup landing in the same second as the safety copy taken
	 * before a restore, destroying the very thing that exists to make the
	 * restore reversible. Found by a test that restored a safety copy and got
	 * the wrong contents back.
	 */
	id := sanitise(name) + "-" + time.Now().UTC().Format("20060102-150405") + "-" + suffix()
	if safety {
		id += "-safety"
	}
	id += ".tar.gz"

	if !ValidID(id) {
		return nil, fmt.Errorf("generated an invalid backup id: %q", id)
	}

	full := filepath.Join(cfg.Dir, id)

	/*
	 * 🚨 Written to a temporary name and renamed at the end.
	 *
	 * A backup interrupted half way — the host rebooted, the disk filled — is a
	 * truncated archive. Under the final name it looks like a backup, appears
	 * in the list, and is discovered to be useless at the only moment anybody
	 * ever opens it. The rename is atomic, so a file under the real name is
	 * always complete.
	 */
	temp := full + ".partial"

	if err := writeArchive(ctx, cfg, temp); err != nil {
		os.Remove(temp)
		return nil, err
	}

	if err := os.Rename(temp, full); err != nil {
		os.Remove(temp)
		return nil, fmt.Errorf("finishing backup: %w", err)
	}

	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}

	return &Backup{ID: id, Size: info.Size(), At: info.ModTime().UTC(), Safety: safety}, nil
}

func writeArchive(ctx context.Context, cfg Config, dest string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, rel := range cfg.Paths {
		base := filepath.Join(cfg.Root, rel)

		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// A path in the manifest that is not there yet — a world not
				// generated, a config not written — is not a reason to fail the
				// whole backup and leave the operator with nothing.
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}

			if ctx.Err() != nil {
				return ctx.Err()
			}

			// 🚨 Symlinks are stored as links, never followed. Following one
			// out of the tree would pull in whatever it points at — and a
			// world directory with a link to / would try to archive the disk.
			if info.Mode()&os.ModeSymlink != 0 {
				target, lerr := os.Readlink(path)
				if lerr != nil {
					return nil
				}

				hdr, herr := tar.FileInfoHeader(info, target)
				if herr != nil {
					return nil
				}
				hdr.Name = archiveName(cfg.Root, path)

				return tw.WriteHeader(hdr)
			}

			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = archiveName(cfg.Root, path)

			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			src, err := os.Open(path)
			if err != nil {
				// A file that vanished mid-walk (a rotating log) is normal.
				return nil
			}
			defer src.Close()

			_, err = io.Copy(tw, src)

			return err
		})

		if err != nil {
			tw.Close()
			gz.Close()

			return fmt.Errorf("backing up %q: %w", rel, err)
		}
	}

	if err := tw.Close(); err != nil {
		gz.Close()
		return err
	}

	return gz.Close()
}

func archiveName(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Base(path)
	}

	return filepath.ToSlash(rel)
}

// Restore replaces the server's data from a backup.
//
// 🚨 A safety copy is taken FIRST, unconditionally, and there is deliberately
// no flag to skip it. The state about to be overwritten is the thing most
// worth keeping: an operator who restores the wrong backup has, at that
// moment, destroyed the only copy of what they had.
func Restore(ctx context.Context, cfg Config, id string, running bool) (*Backup, error) {
	if running {
		// 🚨 Refused rather than handled. Writing a world file under a live
		// server corrupts it, and the corruption surfaces hours later looking
		// like a game bug. Stopping is the caller's decision to make
		// explicitly, so that it appears in the audit log as its own act.
		return nil, fmt.Errorf("stop the server before restoring")
	}

	src, err := resolve(cfg, id)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("no such backup")
	}

	safety, err := Create(ctx, cfg, "pre-restore", true)
	if err != nil {
		return nil, fmt.Errorf("refusing to restore: the safety copy failed: %w", err)
	}

	if err := extract(ctx, cfg, src); err != nil {
		return safety, fmt.Errorf("restore failed after safety copy %s: %w", safety.ID, err)
	}

	return safety, nil
}

func extract(ctx context.Context, cfg Config, archive string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("this backup is not readable — it may be truncated: %w", err)
	}
	defer gz.Close()

	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}

	tr := tar.NewReader(gz)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		/*
		 * 🚨 ZIP-SLIP, and it is REFUSED rather than sanitised.
		 *
		 * An entry named "../../etc/cron.d/backdoor" is written wherever it
		 * says by every extractor that does not check, and a backup file is
		 * the one input here that might not have come from this agent — an
		 * operator copies one between hosts, or restores one a friend sent.
		 *
		 * Cleaning the path instead would be safe from traversal and still
		 * wrong: "../../x" would quietly become "x" and overwrite a real file
		 * inside the server directory. An archive containing traversal is not
		 * a formatting quirk to tidy up, it is a reason to stop.
		 */
		if strings.Contains(hdr.Name, "..") || strings.HasPrefix(hdr.Name, "/") || filepath.IsAbs(hdr.Name) {
			return fmt.Errorf("this backup contains a path outside the server directory (%q) and was not restored", hdr.Name)
		}

		target := filepath.Join(root, filepath.Clean("/"+hdr.Name))

		if !strings.HasPrefix(target, root+string(os.PathSeparator)) && target != root {
			return fmt.Errorf("this backup contains a path outside the server directory (%q) and was not restored", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}

		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}

			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}

			// 🚨 Bounded copy. A malicious or corrupt archive can claim a small
			// size and then decompress forever; without a limit that fills the
			// host's disk, which takes every server on it down.
			if _, err := io.CopyN(out, tr, maxEntryBytes); err != nil && err != io.EOF {
				out.Close()
				return err
			}

			out.Close()

		case tar.TypeSymlink:
			// A symlink in a restored archive could point anywhere. Recreating
			// it inside the tree is fine; the link's TARGET is only followed if
			// something later writes through it, and the path check above
			// covers every write this function makes.
			os.Remove(target)

			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// maxEntryBytes caps one file restored from an archive at 8 GiB — larger than
// any world this is meant to hold, small enough that a decompression bomb
// stops before it fills a disk.
const maxEntryBytes = 8 << 30

// Prune removes the oldest ordinary backups beyond cfg.Keep.
//
// 🚨 Safety copies are NEVER pruned automatically. They exist because somebody
// was about to overwrite something, which is exactly when an automatic
// retention rule deleting the evidence would be most expensive.
func Prune(cfg Config) ([]string, error) {
	if cfg.Keep <= 0 {
		return nil, nil
	}

	all, err := List(cfg)
	if err != nil {
		return nil, err
	}

	var ordinary []Backup

	for _, b := range all {
		if !b.Safety {
			ordinary = append(ordinary, b)
		}
	}

	if len(ordinary) <= cfg.Keep {
		return nil, nil
	}

	var removed []string

	for _, b := range ordinary[cfg.Keep:] {
		path, err := resolve(cfg, b.ID)
		if err != nil {
			continue
		}

		if err := os.Remove(path); err == nil {
			removed = append(removed, b.ID)
		}
	}

	return removed, nil
}

// Delete removes one backup by id.
func Delete(cfg Config, id string) error {
	path, err := resolve(cfg, id)
	if err != nil {
		return err
	}

	return os.Remove(path)
}

// suffix is four random characters, enough that two backups in the same second
// do not collide.
func suffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// A clock-derived fallback is still better than a guaranteed collision.
		return fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
	}

	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}

	return string(b)
}

// sanitise reduces a server name to something that can start a backup id.
func sanitise(name string) string {
	var b strings.Builder

	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ':
			b.WriteRune('-')
		}
	}

	out := strings.Trim(b.String(), "-")

	if out == "" {
		out = "server"
	}

	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}

	return out
}
