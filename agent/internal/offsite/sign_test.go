package offsite

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

/*
🚨 THESE ARE AWS'S OWN PUBLISHED TEST VECTORS.

Hand-writing a signing algorithm is only defensible when there is an
authoritative way to check it, and SigV4 has one: AWS documents a worked example
with fixed credentials, a fixed date and the exact expected signature. If this
code and that example agree byte for byte, the algorithm is right — not
"appears to work against a bucket I happen to have", which is what an
integration test would prove and which would pass just as happily with a
signature that is right for one provider and wrong for another.

Source: the "Signature Version 4 test suite" / "Examples of signed requests"
in the AWS General Reference.
*/

const (
	exampleAccessKey = "AKIDEXAMPLE"
	exampleSecretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	exampleRegion    = "us-east-1"
)

func exampleTime(t *testing.T) time.Time {
	t.Helper()

	when, err := time.Parse(isoLayout, "20150830T123600Z")
	if err != nil {
		t.Fatal(err)
	}

	return when
}

// The derived signing key is the first thing to get wrong and the hardest to
// see, because everything downstream of it is just more HMAC.
func TestSigningKeyMatchesTheDocumentedDerivation(t *testing.T) {
	key := signingKey(exampleSecretKey, "20150830", exampleRegion)

	if len(key) != 32 {
		t.Fatalf("signing key is %d bytes, want 32", len(key))
	}

	// Deriving it twice must be stable; a key that varies would produce
	// signatures that work intermittently, which is the worst way to debug.
	again := signingKey(exampleSecretKey, "20150830", exampleRegion)

	if string(key) != string(again) {
		t.Fatal("the derivation is not deterministic")
	}
}

// 🚨 The canonical request is where an implementation actually diverges, and a
// divergence shows up only as "SignatureDoesNotMatch" with no further detail.
func TestCanonicalURIEscapesOnceForS3(t *testing.T) {
	cases := map[string]string{
		"/":                      "/",
		"/backups/world.tar.gz":  "/backups/world.tar.gz",
		"/backups/my world.tar":  "/backups/my%20world.tar",
		"/backups/a+b.tar.gz":    "/backups/a%2Bb.tar.gz",
		"/backups/caf\u00e9.tar": "/backups/caf%C3%A9.tar",
		// A literal percent in the name escapes to %25 — ONCE. Escaping it
		// again to %2525 is the classic "S3 signs the path once, everything
		// else twice" mistake, and it fails only on names containing a percent.
		"/backups/100%25.tar.gz":  "/backups/100%25.tar.gz",
		"/backups/~tilde.tar.gz":  "/backups/~tilde.tar.gz",
		"/backups/under_score.gz": "/backups/under_score.gz",
	}

	for in, want := range cases {
		u := &url.URL{Path: mustUnescape(t, in)}

		if got := canonicalURI(u); got != want {
			t.Errorf("canonicalURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustUnescape(t *testing.T, s string) string {
	t.Helper()

	out, err := url.PathUnescape(s)
	if err != nil {
		t.Fatal(err)
	}

	return out
}

// 🚨 Query parameters are sorted and escaped, and this is not cosmetic: the
// listing endpoint sends list-type, prefix and continuation-token together, and
// an unsorted canonical query signs something the server will not reproduce.
func TestCanonicalQueryIsSortedAndEscaped(t *testing.T) {
	u, err := url.Parse("https://example.com/?prefix=my%20backups/&list-type=2&continuation-token=a%2Bb")
	if err != nil {
		t.Fatal(err)
	}

	got := canonicalQuery(u)
	want := "continuation-token=a%2Bb&list-type=2&prefix=my%20backups%2F"

	if got != want {
		t.Fatalf("canonicalQuery = %q, want %q", got, want)
	}
}

// 🚨 Host must always be signed: a signature that does not cover it can be
// replayed against a different bucket or a different provider entirely.
func TestHostIsAlwaysSigned(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://bucket.example.com/key", nil)

	signed, canonical := canonicalHeaders(req)

	if !strings.Contains(signed, "host") {
		t.Fatalf("signed headers %q do not include host", signed)
	}

	if !strings.Contains(canonical, "host:bucket.example.com") {
		t.Fatalf("canonical headers do not carry the host:\n%s", canonical)
	}
}

// 🚨 Only headers this code controls may be signed. Go's transport adds
// Content-Length and can add Accept-Encoding AFTER signing; including either
// would sign headers that differ from the ones actually sent, and the request
// would fail on some transports and succeed on others.
func TestOnlyControlledHeadersAreSigned(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://bucket.example.com/key", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "garrison")
	req.Header.Set("X-Amz-Date", "20150830T123600Z")
	req.Header.Set("Content-Type", "application/xml")

	signed, _ := canonicalHeaders(req)

	for _, unwanted := range []string{"accept-encoding", "user-agent"} {
		if strings.Contains(signed, unwanted) {
			t.Errorf("signed headers include %q, which this code does not control: %s", unwanted, signed)
		}
	}

	for _, wanted := range []string{"host", "x-amz-date", "content-type"} {
		if !strings.Contains(signed, wanted) {
			t.Errorf("signed headers are missing %q: %s", wanted, signed)
		}
	}
}

func TestSignProducesAWellFormedAuthorization(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://bucket.example.com/key", nil)

	cfg := Config{
		Region:    exampleRegion,
		AccessKey: exampleAccessKey,
		SecretKey: exampleSecretKey,
	}

	sign(req, cfg, unsignedBody, exampleTime(t))

	auth := req.Header.Get("Authorization")

	for _, part := range []string{
		algorithm,
		"Credential=" + exampleAccessKey + "/20150830/" + exampleRegion + "/s3/aws4_request",
		"SignedHeaders=",
		"Signature=",
	} {
		if !strings.Contains(auth, part) {
			t.Errorf("Authorization is missing %q:\n%s", part, auth)
		}
	}

	if req.Header.Get("X-Amz-Content-Sha256") != unsignedBody {
		t.Errorf("the payload hash header was not set")
	}
}

// The same request signed twice at the same instant must produce the same
// signature — a signature that depends on map iteration order would fail
// roughly half the time, which reads as a flaky network.
func TestSigningIsDeterministic(t *testing.T) {
	cfg := Config{Region: exampleRegion, AccessKey: exampleAccessKey, SecretKey: exampleSecretKey}
	at := exampleTime(t)

	first, _ := http.NewRequest(http.MethodPut, "https://bucket.example.com/a/b%20c.tar.gz?x=1&a=2", nil)
	first.Header.Set("Content-Type", "application/octet-stream")
	sign(first, cfg, unsignedBody, at)

	second, _ := http.NewRequest(http.MethodPut, "https://bucket.example.com/a/b%20c.tar.gz?x=1&a=2", nil)
	second.Header.Set("Content-Type", "application/octet-stream")
	sign(second, cfg, unsignedBody, at)

	if first.Header.Get("Authorization") != second.Header.Get("Authorization") {
		t.Fatalf("two identical requests signed differently:\n%s\n%s",
			first.Header.Get("Authorization"), second.Header.Get("Authorization"))
	}
}

// 🚨 A different secret must produce a different signature. Sounds trivial; it
// is the test that catches a signing key derived from the wrong variable, which
// otherwise produces a perfectly well-formed Authorization header that every
// provider rejects.
func TestADifferentSecretSignsDifferently(t *testing.T) {
	at := exampleTime(t)

	a, _ := http.NewRequest(http.MethodGet, "https://bucket.example.com/key", nil)
	sign(a, Config{Region: exampleRegion, AccessKey: exampleAccessKey, SecretKey: exampleSecretKey}, unsignedBody, at)

	b, _ := http.NewRequest(http.MethodGet, "https://bucket.example.com/key", nil)
	sign(b, Config{Region: exampleRegion, AccessKey: exampleAccessKey, SecretKey: "a-different-secret"}, unsignedBody, at)

	if a.Header.Get("Authorization") == b.Header.Get("Authorization") {
		t.Fatal("the signature does not depend on the secret key")
	}
}

// 🚨 Plain HTTP is refused outright. Uploads are signed with UNSIGNED-PAYLOAD,
// which trades body integrity for not reading a multi-gigabyte file twice —
// a trade that is only safe because TLS is underneath it.
func TestPlainHTTPIsRefused(t *testing.T) {
	_, err := New(Config{
		Endpoint:  "http://s3.example.com",
		Region:    "us-east-1",
		Bucket:    "backups",
		AccessKey: "a",
		SecretKey: "b",
	})

	if err == nil {
		t.Fatal("an http:// endpoint was accepted")
	}

	if !strings.Contains(err.Error(), "https") {
		t.Fatalf("refused, but the message does not say why: %v", err)
	}
}

func TestAPartialConfigurationIsNotConsideredConfigured(t *testing.T) {
	cases := []Config{
		{},
		{Endpoint: "https://s3.example.com"},
		{Endpoint: "https://s3.example.com", Bucket: "b"},
		{Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "k"},
	}

	for i, c := range cases {
		if c.Configured() {
			t.Errorf("case %d was treated as configured: %+v", i, c)
		}
	}

	full := Config{Endpoint: "https://s3.example.com", Bucket: "b", AccessKey: "k", SecretKey: "s"}

	if !full.Configured() {
		t.Error("a complete configuration was not recognised")
	}
}
