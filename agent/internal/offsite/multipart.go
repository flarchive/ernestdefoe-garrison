package offsite

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

/*
Multipart upload, for worlds bigger than a single PUT can carry.

🚨 The failure this exists to prevent is the nastiest kind: off-site backups
that work perfectly for a year and then stop, because the world crossed 5 GiB.
Nothing changes in the configuration, nothing changes in the logs except one
error nobody is reading, and the copies quietly stop arriving — until the day
somebody needs one.

🚨 And the abort. An interrupted multipart upload leaves its parts in the bucket
FOR EVER, invisible in the object listing and fully billable. A provider's own
lifecycle rule can clean them up, but that is somebody else's configuration and
it is not on by default anywhere. So every failure path here aborts the upload
before returning, and an abort that itself fails is folded into the error rather
than hidden — because "your backup failed" and "your backup failed and is now
costing you money every month" are different things to be told.
*/

func (s *Store) putMultipart(ctx context.Context, name string, body io.Reader, size int64) error {
	key := s.key(name)

	uploadID, err := s.createUpload(ctx, key)
	if err != nil {
		return err
	}

	parts, err := s.uploadParts(ctx, key, uploadID, body, size)
	if err != nil {
		return s.abort(ctx, key, uploadID, err)
	}

	if err := s.completeUpload(ctx, key, uploadID, parts); err != nil {
		return s.abort(ctx, key, uploadID, err)
	}

	return nil
}

func (s *Store) createUpload(ctx context.Context, key string) (string, error) {
	query := url.Values{"uploads": []string{""}}

	req, err := s.request(ctx, http.MethodPost, key, query, nil)
	if err != nil {
		return "", err
	}

	// 🚨 An explicit zero length. Without it Go may send a chunked body for a
	// POST with no content, and several S3 implementations reject that with a
	// 501 that mentions Transfer-Encoding and nothing about uploads.
	req.ContentLength = 0

	var result struct {
		UploadID string `xml:"UploadId"`
	}

	if err := s.do(req, &result); err != nil {
		return "", err
	}

	if result.UploadID == "" {
		return "", fmt.Errorf("the provider accepted the upload but returned no upload id")
	}

	return result.UploadID, nil
}

type completedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (s *Store) uploadParts(ctx context.Context, key, uploadID string, body io.Reader, size int64) ([]completedPart, error) {
	var (
		parts  []completedPart
		number = 1
		buf    = make([]byte, PartSize)
		sent   int64
	)

	for {
		/*
		 * 🚨 io.ReadFull, not Read. A single Read on a file is allowed to
		 * return fewer bytes than asked for, and a short part in the MIDDLE of
		 * a multipart upload is rejected by S3 — every part but the last must
		 * be at least 5 MiB. Using Read directly produces an upload that works
		 * on a local disk and fails over a network filesystem, which is a
		 * difference nobody will connect to this line.
		 */
		n, err := io.ReadFull(body, buf)

		if n > 0 {
			etag, perr := s.uploadPart(ctx, key, uploadID, number, buf[:n])
			if perr != nil {
				return nil, perr
			}

			parts = append(parts, completedPart{PartNumber: number, ETag: etag})
			number++
			sent += int64(n)
		}

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}

		if err != nil {
			return nil, err
		}
	}

	/*
	 * 🚨 The size is CHECKED, not assumed.
	 *
	 * A backup file being rewritten while it is uploaded — a schedule that
	 * overlapped, a hand-run backup at the wrong moment — produces a short
	 * read that looks exactly like a clean end of file. Completing the upload
	 * anyway would put a truncated archive in the bucket under a name that
	 * says it is a backup, and it would list, and it would download, and it
	 * would fail only when somebody tried to restore from it. Refusing here
	 * means the operator finds out today.
	 */
	if sent != size {
		return nil, fmt.Errorf("read %d bytes but the file said %d — it changed while being uploaded", sent, size)
	}

	if len(parts) == 0 {
		return nil, fmt.Errorf("nothing to upload")
	}

	return parts, nil
}

func (s *Store) uploadPart(ctx context.Context, key, uploadID string, number int, chunk []byte) (string, error) {
	query := url.Values{
		"partNumber": []string{strconv.Itoa(number)},
		"uploadId":   []string{uploadID},
	}

	req, err := s.request(ctx, http.MethodPut, key, query, bytes.NewReader(chunk))
	if err != nil {
		return "", err
	}

	req.ContentLength = int64(len(chunk))

	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 4096))

		return "", fmt.Errorf("part %d: %s: %s", number, res.Status, describeS3Error(snippet))
	}

	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))

	etag := res.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("part %d uploaded but the provider returned no ETag", number)
	}

	return etag, nil
}

func (s *Store) completeUpload(ctx context.Context, key, uploadID string, parts []completedPart) error {
	payload := struct {
		XMLName xml.Name        `xml:"CompleteMultipartUpload"`
		Parts   []completedPart `xml:"Part"`
	}{Parts: parts}

	body, err := xml.Marshal(payload)
	if err != nil {
		return err
	}

	query := url.Values{"uploadId": []string{uploadID}}

	req, err := s.request(ctx, http.MethodPost, key, query, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/xml")

	/*
	 * 🚨 A 200 from CompleteMultipartUpload does NOT mean the upload worked.
	 *
	 * S3 holds the connection open while it assembles the object and can send
	 * 200 followed by an <Error> document in the body — the status line is
	 * committed before the outcome is known. A client that checks only the
	 * status code reports success for a failed assembly, which is the single
	 * worst lie this package could tell: an off-site backup that does not
	 * exist, recorded as having been made.
	 */
	var result struct {
		XMLName xml.Name `xml:""`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
		ETag    string   `xml:"ETag"`
	}

	if err := s.do(req, &result); err != nil {
		return err
	}

	if result.Code != "" {
		return fmt.Errorf("the provider reported %s while assembling the upload: %s", result.Code, result.Message)
	}

	if result.XMLName.Local != "CompleteMultipartUploadResult" {
		return fmt.Errorf("the provider answered the upload with an unexpected %q document", result.XMLName.Local)
	}

	return nil
}

// abort cleans up a failed multipart upload, and folds any failure to do so
// into the error the caller sees.
func (s *Store) abort(ctx context.Context, key, uploadID string, cause error) error {
	query := url.Values{"uploadId": []string{uploadID}}

	/*
	 * 🚨 context.WithoutCancel, because the usual reason we are here is that
	 * the caller's context was cancelled — a timeout, a shutdown. Reusing it
	 * would mean the abort is cancelled before it is sent, leaving exactly the
	 * orphaned parts this function exists to prevent, precisely in the case
	 * where they are most likely.
	 */
	req, err := s.request(context.WithoutCancel(ctx), http.MethodDelete, key, query, nil)
	if err != nil {
		return fmt.Errorf("%w (and the incomplete upload could not be cleaned up: %v)", cause, err)
	}

	if err := s.do(req, nil); err != nil {
		return fmt.Errorf("%w (and the incomplete upload is still in the bucket, billable: %v)", cause, err)
	}

	return cause
}
