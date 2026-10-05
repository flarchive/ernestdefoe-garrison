package offsite

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

/*
🚨 SigV4 BY HAND, AND THAT IS A DELIBERATE CHOICE.

The obvious alternative is aws-sdk-go-v2, which is correct, maintained, and
pulls roughly forty modules into a binary that customers run AS ROOT on their
game hosts. This agent has exactly one dependency today, and that is most of
its security story: there is very little third-party code between a compromised
registry and a machine somebody's community lives on.

SigV4 is also unusually safe to implement yourself, because AWS publishes
canonical test vectors for exactly this — see sign_test.go, which checks this
code against them byte for byte. An algorithm with an official conformance
suite is a much better candidate for hand-rolling than one without.

One S3-compatible implementation covers AWS S3, Backblaze B2, Cloudflare R2,
Wasabi, MinIO and every other provider worth naming, so the forty modules would
have bought one protocol.
*/

const (
	algorithm    = "AWS4-HMAC-SHA256"
	terminator   = "aws4_request"
	isoLayout    = "20060102T150405Z"
	dateLayout   = "20060102"
	unsignedBody = "UNSIGNED-PAYLOAD"
)

// sign adds the Authorization header to req, signing it for S3.
//
// 🚨 bodyHash is the hex SHA-256 of the body, or UNSIGNED-PAYLOAD.
//
// UNSIGNED-PAYLOAD exists because SigV4's header authentication needs the whole
// payload hashed BEFORE the request starts, and a world backup can be several
// gigabytes — hashing it means reading the file twice, doubling the I/O on a
// host that is usually already the bottleneck. S3 and every compatible provider
// accept it, and the transport's own integrity check takes over.
//
// The trade is real and it is bounded by Config.requireTLS: over plain HTTP an
// unsigned payload could be altered in flight without the signature noticing,
// so this package refuses to talk to an http:// endpoint at all.
func sign(req *http.Request, cfg Config, bodyHash string, now time.Time) {
	amzDate := now.UTC().Format(isoLayout)
	scopeDate := now.UTC().Format(dateLayout)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)

	if req.Host != "" {
		req.Header.Set("Host", req.Host)
	} else {
		req.Header.Set("Host", req.URL.Host)
	}

	signed, canonicalHeaders := canonicalHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signed,
		bodyHash,
	}, "\n")

	scope := strings.Join([]string{scopeDate, cfg.Region, "s3", terminator}, "/")

	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	key := signingKey(cfg.SecretKey, scopeDate, cfg.Region)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization", algorithm+
		" Credential="+cfg.AccessKey+"/"+scope+
		", SignedHeaders="+signed+
		", Signature="+signature)
}

func signingKey(secret, date, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte("s3"))

	return hmacSHA256(k, []byte(terminator))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)

	return h.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// canonicalHeaders returns the signed-header list and the canonical block.
//
// 🚨 Host is always signed. A signature that does not cover Host can be
// replayed against a DIFFERENT bucket or endpoint, which is the whole reason
// the spec insists on it.
func canonicalHeaders(req *http.Request) (string, string) {
	names := make([]string, 0, len(req.Header)+1)
	values := make(map[string]string, len(req.Header)+1)

	for name := range req.Header {
		lower := strings.ToLower(name)

		// Only sign what we control. Go's transport adds Content-Length and
		// may add Accept-Encoding after signing; including either here would
		// produce a signature over headers that differ from the ones sent.
		if lower != "host" && !strings.HasPrefix(lower, "x-amz-") && lower != "content-type" {
			continue
		}

		names = append(names, lower)
		values[lower] = strings.TrimSpace(req.Header.Get(name))
	}

	if _, ok := values["host"]; !ok {
		names = append(names, "host")
		values["host"] = req.URL.Host
	}

	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString(":")
		b.WriteString(collapseSpaces(values[n]))
		b.WriteString("\n")
	}

	return strings.Join(names, ";"), b.String()
}

// collapseSpaces folds runs of whitespace, which the canonical form requires
// for header values.
func collapseSpaces(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

/*
🚨 The path is escaped ONCE for S3, not twice as every other AWS service wants.

This is the single most common way a hand-written SigV4 goes wrong, and it goes
wrong invisibly: every request with a plain key works, and the first backup
whose name contains a character needing escaping gets a 403 that says
"SignatureDoesNotMatch" and nothing about paths. Backup ids are constrained
enough that it might never have surfaced in testing here — which is exactly why
it is written down rather than discovered later by a customer.
*/
func canonicalURI(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}

	parts := strings.Split(u.Path, "/")
	for i, p := range parts {
		parts[i] = escapePath(p)
	}

	return strings.Join(parts, "/")
}

func canonicalQuery(u *url.URL) string {
	q := u.Query()

	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		vs := q[k]
		sort.Strings(vs)
		for _, v := range vs {
			pairs = append(pairs, escapePath(k)+"="+escapePath(v))
		}
	}

	return strings.Join(pairs, "&")
}

// escapePath percent-encodes everything except the unreserved set.
//
// 🚨 net/url's Escape functions are not usable here: QueryEscape turns a space
// into "+", and PathEscape leaves several characters that AWS expects encoded.
// The canonical form is specified as RFC 3986 unreserved characters only.
func escapePath(s string) string {
	const upper = "0123456789ABCDEF"

	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]

		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}

		b.WriteByte('%')
		b.WriteByte(upper[c>>4])
		b.WriteByte(upper[c&15])
	}

	return b.String()
}
