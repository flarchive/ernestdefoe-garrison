package provision

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

/*
Installing a game that does not come from Steam.

🚨 Minecraft, Factorio and Terraria are the reason this exists. None of them is
on SteamCMD — Minecraft is a jar from Mojang, Factorio a tarball from its own
site — and until now Garrison could run all three and install none of them,
which is a strange thing to have to explain to somebody who just wants a
Minecraft server.

🚨 A download is not the same trust as a SteamCMD app id.

An app id names a thing inside Valve's own catalogue. A URL names anywhere on
the internet. So this is deliberately narrow: HTTPS only, a size ceiling, a
timeout, and an unpacker that refuses any archive entry which would write
outside the directory it was given. The URLs that ship with Garrison are in the
catalogue's own source; an operator writing their own template is choosing to
trust whatever they typed, and should be able to see exactly what that allows.
*/

// The largest thing Garrison will pull down for one server. Generous for a
// game server, small enough that a wrong URL cannot fill a disk unnoticed.
const maxDownload = 8 << 30 // 8 GiB

var fetchClient = &http.Client{Timeout: 2 * time.Hour}

/*
resolveDownload turns a template's Download field into a real URL.

🚨 Tokens exist because some downloads have no stable address. Mojang publishes
a different URL for every Minecraft release, so a literal link in the catalogue
would install a version that was current on the day it was written and then rot
silently. The token is resolved at INSTALL time, against the publisher's own
manifest, so "minecraft" means "the current release" for ever.
*/
func resolveDownload(ctx context.Context, spec string) (string, error) {
	switch spec {
	case "mojang:release":
		return latestMinecraftServer(ctx)
	case "factorio:stable":
		// Factorio's own permanent alias for the current headless build. It
		// redirects; the client follows it.
		return "https://factorio.com/get-download/stable/headless/linux64", nil
	}

	return spec, nil
}

// latestMinecraftServer asks Mojang which server jar is current.
func latestMinecraftServer(ctx context.Context) (string, error) {
	const manifest = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

	var top struct {
		Latest struct {
			Release string `json:"release"`
		} `json:"latest"`
		Versions []struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"versions"`
	}

	if err := getJSON(ctx, manifest, &top); err != nil {
		return "", fmt.Errorf("could not ask Mojang which version is current: %w", err)
	}

	var versionURL string

	for _, v := range top.Versions {
		if v.ID == top.Latest.Release {
			versionURL = v.URL
			break
		}
	}

	if versionURL == "" {
		return "", fmt.Errorf("Mojang's manifest named %q as current but did not list it", top.Latest.Release)
	}

	var version struct {
		Downloads struct {
			Server struct {
				URL string `json:"url"`
			} `json:"server"`
		} `json:"downloads"`
	}

	if err := getJSON(ctx, versionURL, &version); err != nil {
		return "", fmt.Errorf("could not read Mojang's entry for %s: %w", top.Latest.Release, err)
	}

	if version.Downloads.Server.URL == "" {
		/*
		 * 🚨 Said plainly rather than reported as a network error. Mojang does
		 * not publish a server jar for every release — a snapshot, or an old
		 * version predating dedicated servers, simply has no `server` download
		 * — and "no server jar for 1.x" tells an operator something true they
		 * can act on.
		 */
		return "", fmt.Errorf("Mojang publishes no server download for %s", top.Latest.Release)
	}

	return version.Downloads.Server.URL, nil
}

func getJSON(ctx context.Context, rawURL string, into any) error {
	body, err := open(ctx, rawURL)
	if err != nil {
		return err
	}
	defer body.Close()

	return json.NewDecoder(io.LimitReader(body, 8<<20)).Decode(into)
}

// open performs the GET, refusing anything that is not plain HTTPS.
func open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%q is not a URL: %w", rawURL, err)
	}

	/*
	 * 🚨 HTTPS only, and checked on the PARSED url rather than by looking at
	 * the string. A download that can be intercepted is an install that can be
	 * replaced, and this one ends in a process running on the operator's host.
	 */
	if u.Scheme != "https" {
		return nil, fmt.Errorf("refusing to download over %q; Garrison fetches over https only", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	res, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		res.Body.Close()

		return nil, fmt.Errorf("%s returned %s", u.Host, res.Status)
	}

	return res.Body, nil
}

/*
fetchInto downloads a template's payload into dir.

`archive` says how to treat it: "tar.gz", "tar.xz", "zip", or empty for a plain
file saved under `as`.
*/
func fetchInto(ctx context.Context, rawURL, archive, as, dir string, progress func(string)) error {
	progress("downloading " + rawURL)

	body, err := open(ctx, rawURL)
	if err != nil {
		return err
	}
	defer body.Close()

	limited := io.LimitReader(body, maxDownload)

	if archive == "" {
		name := as
		if name == "" {
			name = "download"
		}

		return saveFile(limited, filepath.Join(dir, name))
	}

	tmp, err := os.CreateTemp("", "garrison-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	n, err := io.Copy(tmp, limited)
	if err != nil {
		return fmt.Errorf("download failed after %d bytes: %w", n, err)
	}

	if n >= maxDownload {
		return fmt.Errorf("download exceeded the %d byte ceiling; refusing it", int64(maxDownload))
	}

	progress(fmt.Sprintf("downloaded %d bytes, unpacking", n))

	return unpack(tmp.Name(), archive, dir)
}

func saveFile(r io.Reader, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, r)

	return err
}

func unpack(archivePath, kind, dir string) error {
	switch kind {
	case "zip":
		return unzip(archivePath, dir)
	case "tar.gz", "tar.xz":
		return untar(archivePath, kind, dir)
	}

	return fmt.Errorf("unknown archive kind %q", kind)
}

/*
safeJoin is the only way an archive entry becomes a path.

🚨 This is the zip-slip guard, and it is the reason unpacking is written out
rather than shelled to `tar`. An archive is attacker-supplied data even when
the URL is not: an entry named `../../etc/cron.d/x` extracts outside the
directory unless something refuses it, and "the file came from the official
site" is not a check.
*/
func safeJoin(dir, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("archive entry %q is an absolute path", name)
	}

	clean := filepath.Clean(filepath.Join(dir, name))

	if clean != dir && !strings.HasPrefix(clean, dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry %q escapes the install directory", name)
	}

	return clean, nil
}

func unzip(archivePath, dir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target, jerr := safeJoin(dir, f.Name)
		if jerr != nil {
			return jerr
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}

		rc, oerr := f.Open()
		if oerr != nil {
			return oerr
		}

		err = saveFile(io.LimitReader(rc, maxDownload), target)
		rc.Close()

		if err != nil {
			return err
		}
	}

	return nil
}

func untar(archivePath, kind, dir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var src io.Reader = f

	switch kind {
	case "tar.gz":
		gz, gerr := gzip.NewReader(f)
		if gerr != nil {
			return gerr
		}
		defer gz.Close()

		src = gz
	case "tar.xz":
		/*
		 * 🚨 Shelled out, because the standard library has no xz and Factorio
		 * ships .tar.xz. Only ever `xz -d` reading stdin and writing stdout —
		 * no filename reaches the command line, so a hostile archive name
		 * cannot become an argument.
		 */
		xz, lerr := exec.LookPath("xz")
		if lerr != nil {
			return fmt.Errorf("this download is .tar.xz and `xz` is not installed on this host: %w", lerr)
		}

		cmd := exec.Command(xz, "-dc")
		cmd.Stdin = f

		out, perr := cmd.StdoutPipe()
		if perr != nil {
			return perr
		}

		if serr := cmd.Start(); serr != nil {
			return serr
		}

		defer cmd.Wait()

		src = out
	}

	tr := tar.NewReader(src)

	for {
		h, rerr := tr.Next()
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}

		target, jerr := safeJoin(dir, h.Name)
		if jerr != nil {
			return jerr
		}

		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
				return err
			}

			if err := saveFile(io.LimitReader(tr, maxDownload), target); err != nil {
				return err
			}

			// Keep the executable bit; a headless server is usually a script
			// or a binary, and losing it makes the install look complete and
			// refuse to start.
			if h.FileInfo().Mode()&0o111 != 0 {
				if err := os.Chmod(target, 0o750); err != nil {
					return err
				}
			}
		default:
			/*
			 * 🚨 Symlinks, devices and hard links are skipped rather than
			 * recreated. A symlink inside an archive is the same escape as a
			 * `..` path by another route, and no game server needs one to be
			 * unpacked from its distribution tarball.
			 */
			continue
		}
	}
}
