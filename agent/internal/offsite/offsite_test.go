package offsite

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeS3 is enough of the protocol to exercise the paths that matter, and
// nothing more. A real bucket would test the provider; this tests this code.
type fakeS3 struct {
	mu sync.Mutex

	objects map[string][]byte
	parts   map[string][]byte

	requests []string

	// completeBody, when set, is returned from CompleteMultipartUpload with a
	// 200 — the trap this package exists to survive.
	completeBody string

	aborted bool
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string][]byte{}, parts: map[string][]byte{}}
}

func (f *fakeS3) start(t *testing.T) *httptest.Server {
	t.Helper()

	// 🚨 TLS, because the package refuses plain HTTP — and that refusal is a
	// feature worth keeping honest in the tests rather than working around.
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)

	return srv
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)

	q := r.URL.Query()
	body, _ := io.ReadAll(r.Body)

	switch {
	case r.Method == http.MethodPost && q.Has("uploads"):
		fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)

	case r.Method == http.MethodPut && q.Has("partNumber"):
		key := r.URL.Path + ":" + q.Get("partNumber")
		f.parts[key] = body
		w.Header().Set("ETag", `"etag-`+q.Get("partNumber")+`"`)

	case r.Method == http.MethodPost && q.Has("uploadId"):
		if f.completeBody != "" {
			// 🚨 200 with an error document, exactly as S3 does when assembly
			// fails after the status line is already committed.
			fmt.Fprint(w, f.completeBody)

			return
		}

		var assembled []byte
		for i := 1; ; i++ {
			part, ok := f.parts[r.URL.Path+":"+fmt.Sprint(i)]
			if !ok {
				break
			}
			assembled = append(assembled, part...)
		}

		f.objects[r.URL.Path] = assembled
		fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"done"</ETag></CompleteMultipartUploadResult>`)

	case r.Method == http.MethodDelete && q.Has("uploadId"):
		f.aborted = true

	case r.Method == http.MethodPut:
		f.objects[r.URL.Path] = body

	case r.Method == http.MethodDelete:
		delete(f.objects, r.URL.Path)

	case r.Method == http.MethodGet && q.Get("list-type") == "2":
		var b strings.Builder
		b.WriteString(`<ListBucketResult><IsTruncated>false</IsTruncated>`)
		for key, v := range f.objects {
			fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>2026-09-15T12:00:00.000Z</LastModified></Contents>`,
				strings.TrimPrefix(key, "/backups/"), len(v))
		}
		b.WriteString(`</ListBucketResult>`)
		fmt.Fprint(w, b.String())

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func store(t *testing.T, srv *httptest.Server, cfg Config) *Store {
	t.Helper()

	cfg.Endpoint = srv.URL
	cfg.Region = "us-east-1"
	cfg.Bucket = "backups"
	cfg.AccessKey = "k"
	cfg.SecretKey = "s"
	cfg.PathStyle = true

	// httptest's certificate is self-signed; validate() has already had its
	// say about the scheme, which is the part that matters.
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	s.client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}

	return s
}

func tempFile(t *testing.T, size int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "world.tar.gz")

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestASmallBackupGoesUpInOneRequest(t *testing.T) {
	f := newFakeS3()
	srv := f.start(t)
	s := store(t, srv, Config{})

	if err := s.Put(context.Background(), "world.tar.gz", tempFile(t, 1024)); err != nil {
		t.Fatal(err)
	}

	if len(f.objects) != 1 {
		t.Fatalf("stored %d objects, want 1", len(f.objects))
	}

	for _, r := range f.requests {
		if strings.Contains(r, "uploads") {
			t.Fatalf("a small file used multipart: %v", f.requests)
		}
	}
}

// 🚨 The failure this prevents: off-site backups that work for a year and stop
// the day the world crosses S3's single-PUT limit.
func TestALargeBackupIsUploadedInParts(t *testing.T) {
	f := newFakeS3()
	srv := f.start(t)
	s := store(t, srv, Config{Prefix: "srv"})

	size := MultipartThreshold + PartSize + 7
	path := tempFile(t, size)

	if err := s.Put(context.Background(), "world.tar.gz", path); err != nil {
		t.Fatal(err)
	}

	stored, ok := f.objects["/backups/srv/world.tar.gz"]
	if !ok {
		t.Fatalf("nothing arrived under the prefix; got %v", keys(f.objects))
	}

	want, _ := os.ReadFile(path)

	if len(stored) != len(want) {
		t.Fatalf("reassembled %d bytes, uploaded %d", len(stored), len(want))
	}

	for i := range want {
		if stored[i] != want[i] {
			t.Fatalf("the reassembled object differs at byte %d — parts were ordered or sized wrongly", i)
		}
	}
}

/*
🚨 THE ONE THAT MATTERS MOST IN THIS FILE.

S3 answers CompleteMultipartUpload with 200 and then an <Error> document in the
body, because it commits the status line before it knows whether assembly
worked. A client that checks only the status code reports a successful backup
that does not exist — the worst lie this package could tell, and one that is
only discovered by somebody trying to restore from it.
*/
func TestA200WithAnErrorBodyIsAFailure(t *testing.T) {
	f := newFakeS3()
	f.completeBody = `<Error><Code>InternalError</Code><Message>We encountered an internal error.</Message></Error>`

	srv := f.start(t)
	s := store(t, srv, Config{})

	err := s.Put(context.Background(), "world.tar.gz", tempFile(t, MultipartThreshold+10))

	if err == nil {
		t.Fatal("a 200 carrying an error document was reported as success")
	}

	if !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("failed, but without the provider's reason: %v", err)
	}
}

// 🚨 An interrupted multipart upload leaves its parts in the bucket for ever,
// invisible in a listing and fully billable.
func TestAFailedUploadIsAborted(t *testing.T) {
	f := newFakeS3()
	f.completeBody = `<Error><Code>InternalError</Code><Message>nope</Message></Error>`

	srv := f.start(t)
	s := store(t, srv, Config{})

	_ = s.Put(context.Background(), "world.tar.gz", tempFile(t, MultipartThreshold+10))

	if !f.aborted {
		t.Fatal("the failed upload was left incomplete in the bucket")
	}
}

func TestPruneKeepsTheNewestAndDeletesTheRest(t *testing.T) {
	f := newFakeS3()
	srv := f.start(t)
	s := store(t, srv, Config{Keep: 2})

	for i := 0; i < 5; i++ {
		if err := s.Put(context.Background(), fmt.Sprintf("world-%d.tar.gz", i), tempFile(t, 64)); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(removed) != 3 {
		t.Fatalf("removed %v, want three of the five", removed)
	}

	if len(f.objects) != 2 {
		t.Fatalf("%d objects left, want 2: %v", len(f.objects), keys(f.objects))
	}
}

func TestPruneWithNoLimitDeletesNothing(t *testing.T) {
	f := newFakeS3()
	srv := f.start(t)
	s := store(t, srv, Config{Keep: 0})

	for i := 0; i < 3; i++ {
		_ = s.Put(context.Background(), fmt.Sprintf("world-%d.tar.gz", i), tempFile(t, 64))
	}

	removed, err := s.Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if removed != nil {
		t.Fatalf("removed %v with no retention configured", removed)
	}

	if len(f.objects) != 3 {
		t.Fatalf("%d objects left, want all 3", len(f.objects))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// 🚨 The provider's diagnosis, not its XML. This string ends up on a forum
// page in front of somebody trying to work out why their backups are not
// arriving, and every S3 failure puts the whole answer in <Code>.
func TestAProviderErrorIsReducedToItsDiagnosis(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match the signature you provided.</Message><Key>x</Key><BucketName>b</BucketName><Resource>/b/x</Resource></Error>`)

	got := describeS3Error(body)

	if !strings.HasPrefix(got, "SignatureDoesNotMatch: ") {
		t.Fatalf("got %q, want it to lead with the code", got)
	}

	if strings.Contains(got, "<Error>") || strings.Contains(got, "BucketName") {
		t.Fatalf("the raw XML leaked through: %q", got)
	}
}

// A proxy or load balancer in front of the bucket may answer with HTML, and
// its words are still better than nothing.
func TestANonXMLErrorIsPassedThrough(t *testing.T) {
	got := describeS3Error([]byte("  502 Bad Gateway  "))

	if got != "502 Bad Gateway" {
		t.Fatalf("got %q", got)
	}
}
