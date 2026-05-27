package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchMetadataAndDownloadPackage(t *testing.T) {
	const payload = "Package metadata\n"
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metadata" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(0))
	expected := checksumFor(payload)

	data, err := client.FetchMetadata(context.Background(), server.URL+"/metadata", &expected)
	if err != nil {
		t.Fatalf("FetchMetadata returned error: %v", err)
	}
	if string(data) != payload {
		t.Fatalf("unexpected metadata payload: %q", data)
	}

	destination := filepath.Join(t.TempDir(), "pool", "package.deb")
	if err := client.DownloadPackage(context.Background(), server.URL+"/metadata", destination, &expected); err != nil {
		t.Fatalf("DownloadPackage returned error: %v", err)
	}

	written, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(written) != payload {
		t.Fatalf("unexpected downloaded payload: %q", written)
	}
}

func TestHTTP404DoesNotRetry(t *testing.T) {
	var attempts int
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(3), WithRetryDelay(0))
	_, err := client.FetchMetadata(context.Background(), server.URL+"/missing", nil)
	if err == nil {
		t.Fatalf("expected 404 error")
	}

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected HTTP 404 error, got %T %v", err, err)
	}
	if attempts != 1 {
		t.Fatalf("expected no retry for 404, got %d attempts", attempts)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	var attempts int
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(3), WithRetryDelay(0))
	data, err := client.FetchMetadata(context.Background(), server.URL+"/metadata", nil)
	if err != nil {
		t.Fatalf("FetchMetadata returned error: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("unexpected payload: %q", data)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryExhaustion(t *testing.T) {
	var attempts int
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "temporary", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(2), WithRetryDelay(0))
	_, err := client.FetchMetadata(context.Background(), server.URL+"/metadata", nil)
	if err == nil {
		t.Fatalf("expected retry exhaustion error")
	}

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 error, got %T %v", err, err)
	}
	if attempts != 3 {
		t.Fatalf("expected first attempt plus 2 retries, got %d", attempts)
	}
}

func TestSizeMismatchIsHardError(t *testing.T) {
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "payload")
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(3), WithRetryDelay(0))
	_, err := client.FetchMetadata(context.Background(), server.URL+"/metadata", &Checksum{Size: 999})
	if err == nil {
		t.Fatalf("expected size mismatch")
	}
	if !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected size mismatch error, got %v", err)
	}
}

func TestChecksumMismatchIsHardError(t *testing.T) {
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "payload")
	}))
	defer server.Close()

	expected := checksumFor("payload")
	expected.SHA256 = strings.Repeat("0", 64)

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(3), WithRetryDelay(0))
	err := client.DownloadPackage(context.Background(), server.URL+"/package.deb", filepath.Join(t.TempDir(), "package.deb"), &expected)
	if err == nil {
		t.Fatalf("expected checksum mismatch")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch error, got %v", err)
	}
}

func TestGetLength(t *testing.T) {
	const payload = "payload"
	server := newLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, payload)
		}
	}))
	defer server.Close()

	client := NewClient(WithHTTPClient(server.Client()), WithRetries(0))
	length, err := client.GetLength(context.Background(), server.URL+"/package.deb")
	if err != nil {
		t.Fatalf("GetLength returned error: %v", err)
	}
	if length != int64(len(payload)) {
		t.Fatalf("expected length %d, got %d", len(payload), length)
	}
}

func TestDownloaderInterface(t *testing.T) {
	var _ Downloader = NewClient(WithTimeout(time.Second))
}

type testServer struct {
	URL     string
	handler http.Handler
}

func newLocalServer(t *testing.T, handler http.Handler) *testServer {
	t.Helper()
	return &testServer{
		URL:     "http://mirror.test",
		handler: handler,
	}
}

func (s *testServer) Close() {}

func (s *testServer) Client() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			recorder := httptest.NewRecorder()
			s.handler.ServeHTTP(recorder, req)

			response := recorder.Result()
			response.Request = req
			if response.Body == nil {
				response.Body = io.NopCloser(bytes.NewReader(nil))
			}
			return response, nil
		}),
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func checksumFor(value string) Checksum {
	writer := newChecksumWriter()
	_, _ = writer.Write([]byte(value))
	return writer.Sum()
}
