package discover

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// server answers with handlers[0] for the first request, handlers[1] for
// the second and so on, repeating the last; it counts requests.
func server(t *testing.T, handlers ...http.HandlerFunc) (url string, requests *atomic.Int32) {
	t.Helper()
	requests = new(atomic.Int32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1))
		handlers[min(n, len(handlers))-1](w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/x", requests
}

func ok(w http.ResponseWriter, r *http.Request) { w.Write([]byte("body")) }

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }
}

// cutOff promises 100 bytes, sends 4 and drops the connection.
func cutOff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "100")
	w.Write([]byte("part"))
	w.(http.Flusher).Flush()
	conn, _, _ := w.(http.Hijacker).Hijack()
	conn.Close()
}

// slow sends headers and part of the body, then stalls past the timeout.
func slow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "100")
	w.Write([]byte("part"))
	w.(http.Flusher).Flush()
	select {
	case <-r.Context().Done():
	case <-time.After(5 * time.Second):
	}
}

func fetcher(timeout time.Duration) *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: timeout}, retryDelay: time.Millisecond}
}

func TestFetch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		handlers []http.HandlerFunc
		requests int32
		wantErr  string // empty: success
	}{
		{"ok", []http.HandlerFunc{ok}, 1, ""},
		{"503 then ok", []http.HandlerFunc{status(503), ok}, 2, ""},
		{"503 twice", []http.HandlerFunc{status(503)}, 2, "the server answered 503 Service Unavailable"},
		{"429 then ok", []http.HandlerFunc{status(429), ok}, 2, ""},
		{"404 is not retried", []http.HandlerFunc{status(404), ok}, 1, "the server answered 404 Not Found"},
		{"403 is not retried", []http.HandlerFunc{status(403), ok}, 1, "the server answered 403 Forbidden"},
		{"cut off then ok", []http.HandlerFunc{cutOff, ok}, 2, ""},
		{"cut off twice", []http.HandlerFunc{cutOff}, 2, "the connection was cut off"},
		{"slow body is not retried", []http.HandlerFunc{slow, ok}, 1, "no complete answer within 300ms (raise it with --timeout)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, n := server(t, tc.handlers...)
			body, err := fetcher(300*time.Millisecond).Fetch(context.Background(), "the test page", u)
			switch {
			case tc.wantErr == "" && (err != nil || string(body) != "body"):
				t.Fatalf("got %q, %v", body, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			case tc.wantErr != "" && !strings.HasPrefix(err.Error(), "couldn't load the test page from 127.0.0.1:"):
				t.Fatalf("err = %v, want it to say what failed and where", err)
			}
			if got := n.Load(); got != tc.requests {
				t.Errorf("%d requests, want %d", got, tc.requests)
			}
		})
	}
}

func TestFetchNoAnswer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // accepts, never answers
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	start := time.Now()
	_, err = fetcher(300*time.Millisecond).Fetch(context.Background(), "the test page", "http://"+ln.Addr().String()+"/")
	if err == nil || !strings.Contains(err.Error(), "no complete answer within 300ms") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v: a timeout must not be retried", d)
	}
}

func TestFetchRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now
	_, err = fetcher(time.Second).Fetch(context.Background(), "the test page", "http://"+addr+"/")
	if err == nil || !strings.Contains(err.Error(), "couldn't connect to the server") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchCertificates(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(ok))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // rejected handshakes are the point
	srv.StartTLS()
	defer srv.Close()
	// The test server's certificate isn't trusted by the default client.
	_, err := fetcher(time.Second).Fetch(context.Background(), "the test page", srv.URL)
	if err == nil || !strings.Contains(err.Error(), "isn't from a trusted authority") {
		t.Fatalf("untrusted: err = %v", err)
	}
	// Trusted, but issued for 127.0.0.1 and example.com, not localhost.
	f := &Fetcher{Client: srv.Client(), retryDelay: time.Millisecond}
	f.Client.Timeout = time.Second
	_, err = f.Fetch(context.Background(), "the test page", strings.Replace(srv.URL, "127.0.0.1", "localhost", 1))
	if err == nil || !strings.Contains(err.Error(), "is for a different site") {
		t.Fatalf("wrong host: err = %v", err)
	}
}

func TestFetchTooLarge(t *testing.T) {
	big := strings.Repeat("x", MaxBody+1)
	u, n := server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(big)) })
	if _, err := fetcher(5*time.Second).Fetch(context.Background(), "the test page", u); err == nil || !strings.Contains(err.Error(), "larger than 16 MiB") {
		t.Fatalf("oversized body: err = %v", err)
	}
	if n.Load() != 1 {
		t.Errorf("%d requests: an oversized body must not be retried", n.Load())
	}
	at := strings.Repeat("x", MaxBody)
	u, _ = server(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(at)) })
	if b, err := fetcher(5*time.Second).Fetch(context.Background(), "the test page", u); err != nil || len(b) != MaxBody {
		t.Fatalf("body at the limit: %d bytes, %v", len(b), err)
	}
}

// TestFetchCancelDuringRetry checks Ctrl+C during the pause before a retry
// returns at once with the context's error, which main treats as an
// interrupt rather than a failure.
func TestFetchCancelDuringRetry(t *testing.T) {
	u, n := server(t, status(503))
	f := fetcher(time.Second)
	f.retryDelay = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	f.Logf = func(string, ...any) { cancel() } // cancel as the retry is announced
	start := time.Now()
	_, err := f.Fetch(ctx, "the test page", u)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second || n.Load() != 1 {
		t.Fatalf("returned after %v and %d requests", d, n.Load())
	}
}
