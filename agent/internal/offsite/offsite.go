// Package offsite copies backups to S3-compatible storage.
//
// 🚨 THE AGENT UPLOADS, NOT THE FORUM, AND THE CREDENTIALS NEVER LEAVE THE HOST.
//
// The tempting design is to put the bucket keys in the forum's admin panel, so
// an operator configures everything in one place. It is a worse design for a
// reason that is hard to undo later: the forum is a PHP application on the
// public internet running third-party extension code, and it is the part of
// this system most likely to be compromised. Keys stored there are keys that
// leak with it — and they would then have to cross the poll channel to reach
// the agent, so they would leak from two places instead of none.
//
// So off-site credentials live in the agent's own config file, on the host,
// next to the backup paths they relate to. The forum is told WHETHER copies are
// landing and never how. That is the same boundary as everything else here: the
// forum asks for verbs, the host holds the authority.
package offsite

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Config is what an operator put in the agent's config file for one server.
type Config struct {
	// Endpoint is the S3 API base, e.g. https://s3.us-west-002.backblazeb2.com
	// or https://<account>.r2.cloudflarestorage.com.
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`

	// Prefix is an optional folder inside the bucket. One bucket can then hold
	// several servers without their backups colliding.
	Prefix string `json:"prefix,omitempty"`

	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`

	/*
	 * 🚨 Path style, which most non-AWS providers need.
	 *
	 * AWS serves buckets as <bucket>.s3.amazonaws.com; MinIO, and B2 and R2
	 * depending on how they are set up, serve them as <endpoint>/<bucket>.
	 * Getting this wrong produces a DNS failure or a 404 rather than an auth
	 * error, so it reads as "my endpoint is wrong" and sends people looking in
	 * the wrong place.
	 */
	PathStyle bool `json:"pathStyle,omitempty"`

	// Keep is how many copies to retain off-site. Zero means keep everything —
	// deliberately, because the point of an off-site copy is often that it
	// outlives the local retention.
	Keep int `json:"keep,omitempty"`
}

// Configured reports whether enough was set for uploads to be attempted.
func (c Config) Configured() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

/*
🚨 HTTPS ONLY, and this refusal is not negotiable.

Uploads here are signed with UNSIGNED-PAYLOAD, which trades a second read of a
multi-gigabyte file for trusting the transport to protect the body. Over TLS
that is the standard, safe trade every S3 client makes. Over plain HTTP it means
a backup could be altered in flight and the signature would not notice — so an
operator who typed http:// gets an error saying why, rather than a silently
weaker guarantee they never agreed to.
*/
func (c Config) validate() error {
	if !c.Configured() {
		return fmt.Errorf("off-site storage is not fully configured")
	}

	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return fmt.Errorf("off-site endpoint is not a URL: %w", err)
	}

	if u.Scheme != "https" {
		return fmt.Errorf("off-site endpoint must be https, got %q", u.Scheme)
	}

	if c.Region == "" {
		return fmt.Errorf("off-site region is required (many providers accept \"auto\" or \"us-east-1\")")
	}

	return nil
}

// Object is one file in the bucket.
type Object struct {
	Key  string    `json:"key"`
	Size int64     `json:"size"`
	At   time.Time `json:"at"`
}

// Store talks to one bucket.
type Store struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) (*Store, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &Store{
		cfg: cfg,
		client: &http.Client{
			/*
			 * 🚨 No overall timeout on the CLIENT, because a legitimate upload
			 * of a large world over a slow home connection can run for an hour
			 * and killing it at ten minutes would mean off-site backups that
			 * work in testing and never work in production.
			 *
			 * The per-attempt bound is the caller's context instead, which is
			 * the one place that knows how long the whole operation may take.
			 */
			Transport: &http.Transport{
				// Bounded where it is safe to bound: a provider that will not
				// answer at all should fail fast rather than hold the slot.
				ResponseHeaderTimeout: 60 * time.Second,
				TLSHandshakeTimeout:   20 * time.Second,
			},
		},
	}, nil
}

// MultipartThreshold is when Put switches from one request to many.
//
// 🚨 S3 caps a single PUT at 5 GiB. A Minecraft world with a big map or an ARK
// save can pass that, and the failure without multipart is an upload that
// worked for a year and then silently stopped once the world grew — which is
// the exact shape of failure this whole product exists to prevent.
//
// 64 MiB is well under the cap and comfortably above the size where the extra
// round trips matter.
const MultipartThreshold = 64 << 20

// PartSize is one chunk of a multipart upload. S3 requires at least 5 MiB for
// every part but the last.
const PartSize = 32 << 20

// Put uploads a local file under the configured prefix.
func (s *Store) Put(ctx context.Context, name, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	if info.Size() >= MultipartThreshold {
		return s.putMultipart(ctx, name, f, info.Size())
	}

	req, err := s.request(ctx, http.MethodPut, s.key(name), nil, f)
	if err != nil {
		return err
	}

	req.ContentLength = info.Size()

	return s.do(req, nil)
}

// List returns what is in the bucket under the prefix, newest first.
func (s *Store) List(ctx context.Context) ([]Object, error) {
	query := url.Values{}
	query.Set("list-type", "2")

	if s.cfg.Prefix != "" {
		query.Set("prefix", strings.TrimSuffix(s.cfg.Prefix, "/")+"/")
	}

	var out []Object

	/*
	 * 🚨 PAGED. S3 returns at most a thousand keys per response and says so
	 * with IsTruncated — a client that ignores it sees exactly the first
	 * thousand and reports the rest as absent. For retention that means
	 * quietly never deleting anything past the first page; for a restore it
	 * means an operator being told a backup they can see in the provider's own
	 * console does not exist.
	 */
	for {
		req, err := s.request(ctx, http.MethodGet, "", query, nil)
		if err != nil {
			return nil, err
		}

		var result struct {
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
			Contents              []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
		}

		if err := s.do(req, &result); err != nil {
			return nil, err
		}

		for _, c := range result.Contents {
			out = append(out, Object{Key: c.Key, Size: c.Size, At: c.LastModified.UTC()})
		}

		if !result.IsTruncated || result.NextContinuationToken == "" {
			break
		}

		query.Set("continuation-token", result.NextContinuationToken)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })

	return out, nil
}

// Delete removes one object.
func (s *Store) Delete(ctx context.Context, key string) error {
	req, err := s.request(ctx, http.MethodDelete, key, nil, nil)
	if err != nil {
		return err
	}

	return s.do(req, nil)
}

// Prune deletes the oldest copies beyond Keep, and reports what it removed.
//
// 🚨 Returns the list rather than a count, because "pruned 4" in a log is
// unfalsifiable: nobody can tell afterwards whether it removed the four oldest
// or the four most recent. Naming them makes a retention bug visible in the
// same log line that caused it.
func (s *Store) Prune(ctx context.Context) ([]string, error) {
	if s.cfg.Keep <= 0 {
		return nil, nil
	}

	objects, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	if len(objects) <= s.cfg.Keep {
		return nil, nil
	}

	var removed []string

	for _, o := range objects[s.cfg.Keep:] {
		if err := s.Delete(ctx, o.Key); err != nil {
			// 🚨 Reported, not aborted. One object that will not delete — a
			// legal hold, an object-lock policy, a permission the key lacks —
			// must not stop the rest of retention running, or a single stuck
			// file grows the bill for ever.
			return removed, fmt.Errorf("deleting %s: %w", o.Key, err)
		}

		removed = append(removed, o.Key)
	}

	return removed, nil
}

func (s *Store) key(name string) string {
	if s.cfg.Prefix == "" {
		return name
	}

	return path.Join(strings.Trim(s.cfg.Prefix, "/"), name)
}

// request builds a signed request against the bucket.
func (s *Store) request(ctx context.Context, method, key string, query url.Values, body io.Reader) (*http.Request, error) {
	base, err := url.Parse(s.cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	if s.cfg.PathStyle {
		base.Path = path.Join(base.Path, s.cfg.Bucket)
	} else {
		base.Host = s.cfg.Bucket + "." + base.Host
	}

	if key != "" {
		base.Path = path.Join(base.Path, key)
	}

	if base.Path == "" {
		base.Path = "/"
	}

	if query != nil {
		base.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return nil, err
	}

	sign(req, s.cfg, unsignedBody, time.Now())

	return req, nil
}

/*
describeS3Error turns a provider's XML error into the sentence it is trying to
say.

🚨 Because this string ends up on a forum page, in front of somebody trying to
work out why their backups are not being copied.

Every S3 implementation answers a failure with a document whose <Code> is the
whole diagnosis — SignatureDoesNotMatch means the keys are wrong,
NoSuchBucket means the name or the region is, AccessDenied means the key is
real but lacks a policy, RequestTimeTooSkewed means the host's clock is out.
Passing the raw body through shows the operator a wall of XML containing that
one useful line, and they have to find it. Passing only the status code throws
the diagnosis away entirely.

Falls back to the raw text when it is not XML, because a proxy or a load
balancer in front of the bucket may answer with HTML, and its words are still
better than nothing.
*/
func describeS3Error(body []byte) string {
	var doc struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}

	if err := xml.Unmarshal(body, &doc); err == nil && doc.Code != "" {
		if doc.Message == "" {
			return doc.Code
		}

		return doc.Code + ": " + doc.Message
	}

	return strings.TrimSpace(string(body))
}

// do sends a request and decodes an XML result, if one is wanted.
func (s *Store) do(req *http.Request, into any) error {
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		/*
		 * 🚨 The provider's own error body, truncated, in the message.
		 *
		 * S3 errors are XML with a Code and a Message that say exactly what is
		 * wrong — "SignatureDoesNotMatch", "NoSuchBucket", "AccessDenied",
		 * "RequestTimeTooSkewed" — and each points at a different fix. A
		 * message of "off-site upload failed: 403" points at nothing, and an
		 * operator debugging their own bucket credentials has no other source
		 * of truth to consult.
		 */
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 4096))

		return fmt.Errorf("%s %s: %s: %s", req.Method, req.URL.Path, res.Status, describeS3Error(snippet))
	}

	if into == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))

		return nil
	}

	return xml.NewDecoder(res.Body).Decode(into)
}
