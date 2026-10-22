package offsite

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

/*
🚨 THE TEST THAT JUSTIFIES HAND-WRITING SIGV4.

Everything else in this package is checked against AWS's published algorithm or
against a fake that answers however the test wants. Neither proves the thing
that actually matters: that a REAL S3 implementation, verifying signatures with
its own independent code, accepts what this produces. A signer can satisfy the
specification as you have understood it and still be rejected by every provider.

So this runs against MinIO — the same S3 server people self-host, with the same
signature verification — over real TLS, doing a real multipart upload. It is
skipped unless GARRISON_S3_ENDPOINT is set, so `go test ./...` stays fast and
offline for everyone else, and the command to bring one up is right here:

    openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
      -keyout certs/private.key -out certs/public.crt \
      -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

    docker run -d --name garrison-minio -p 9443:9000 \
      -e MINIO_ROOT_USER=garrisontest -e MINIO_ROOT_PASSWORD=garrisontest123 \
      -v "$PWD/certs:/root/.minio/certs:ro" \
      quay.io/minio/minio server /data --address :9000

    GARRISON_S3_ENDPOINT=https://127.0.0.1:9443 \
    GARRISON_S3_KEY=garrisontest GARRISON_S3_SECRET=garrisontest123 \
    go test ./internal/offsite/ -run Conformance -v
*/

func conformanceStore(t *testing.T, bucket string) *Store {
	t.Helper()

	endpoint := os.Getenv("GARRISON_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set GARRISON_S3_ENDPOINT to run the conformance suite — see the file comment")
	}

	s, err := New(Config{
		Endpoint:  endpoint,
		Region:    "us-east-1",
		Bucket:    bucket,
		AccessKey: os.Getenv("GARRISON_S3_KEY"),
		SecretKey: os.Getenv("GARRISON_S3_SECRET"),
		PathStyle: true,
		Keep:      2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The local MinIO uses a self-signed certificate. The signature
	// verification — the thing under test — is unaffected by that.
	s.client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}

	return s
}

// createBucket is the one thing this package does not otherwise need, so it
// lives in the test rather than widening the Store's surface.
func createBucket(t *testing.T, s *Store, name string) {
	t.Helper()

	req, err := s.request(context.Background(), http.MethodPut, "", nil, bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}

	req.ContentLength = 0

	res, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	// 409 means it already exists, which is fine.
	if res.StatusCode >= 300 && res.StatusCode != http.StatusConflict {
		t.Fatalf("could not create bucket %s: %s", name, res.Status)
	}
}

func TestConformanceRoundTripAgainstARealS3(t *testing.T) {
	bucket := fmt.Sprintf("garrison-%d", time.Now().UnixNano())

	s := conformanceStore(t, bucket)
	createBucket(t, s, bucket)

	// Small: one PUT.
	small := filepath.Join(t.TempDir(), "small.tar.gz")
	if err := os.WriteFile(small, bytes.Repeat([]byte("a"), 2048), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := s.Put(context.Background(), "small.tar.gz", small); err != nil {
		t.Fatalf("a real S3 rejected the small upload — the signature is wrong: %v", err)
	}

	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("listing was rejected: %v", err)
	}

	if len(list) != 1 || list[0].Key != "small.tar.gz" || list[0].Size != 2048 {
		t.Fatalf("listing came back as %+v", list)
	}
}

// 🚨 Multipart separately, because it signs four different request shapes —
// initiate, each part, complete, abort — and each has its own query string.
// A signer that gets the canonical query wrong passes every single-PUT test.
func TestConformanceMultipartAgainstARealS3(t *testing.T) {
	bucket := fmt.Sprintf("garrison-mp-%d", time.Now().UnixNano())

	s := conformanceStore(t, bucket)
	createBucket(t, s, bucket)

	size := MultipartThreshold + PartSize + 13
	path := filepath.Join(t.TempDir(), "world.tar.gz")

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := s.Put(context.Background(), "world.tar.gz", path); err != nil {
		t.Fatalf("a real S3 rejected the multipart upload: %v", err)
	}

	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 1 || list[0].Size != int64(size) {
		t.Fatalf("the reassembled object is %+v, want one of %d bytes", list, size)
	}
}

// 🚨 A name that needs escaping, which is where a hand-written canonical URI
// diverges — and it diverges invisibly, because every plain name works.
func TestConformanceAnAwkwardNameIsSignedCorrectly(t *testing.T) {
	bucket := fmt.Sprintf("garrison-esc-%d", time.Now().UnixNano())

	s := conformanceStore(t, bucket)
	createBucket(t, s, bucket)

	small := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(small, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"shattered pact-20260915-120000-a1b2.tar.gz",
		"café-20260915-120000-a1b2.tar.gz",
		"a+b-20260915-120000-a1b2.tar.gz",
		"100%-20260915-120000-a1b2.tar.gz",
	} {
		if err := s.Put(context.Background(), name, small); err != nil {
			t.Errorf("a real S3 rejected %q: %v", name, err)
		}
	}
}

func TestConformancePruneAgainstARealS3(t *testing.T) {
	bucket := fmt.Sprintf("garrison-prune-%d", time.Now().UnixNano())

	s := conformanceStore(t, bucket)
	createBucket(t, s, bucket)

	small := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(small, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		if err := s.Put(context.Background(), fmt.Sprintf("world-%d.tar.gz", i), small); err != nil {
			t.Fatal(err)
		}
		// Distinct modification times, so "oldest" is well defined.
		time.Sleep(1100 * time.Millisecond)
	}

	removed, err := s.Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(removed) != 3 {
		t.Fatalf("removed %v, want the three oldest of five", removed)
	}

	left, _ := s.List(context.Background())

	if len(left) != 2 {
		t.Fatalf("%d objects left, want 2: %+v", len(left), left)
	}

	// 🚨 And they must be the NEWEST two, not any two. A prune that keeps the
	// wrong end of the list is still "keep 2" and is a catastrophe.
	for _, o := range left {
		if o.Key != "world-4.tar.gz" && o.Key != "world-3.tar.gz" {
			t.Fatalf("kept %s — prune deleted from the wrong end", o.Key)
		}
	}
}
