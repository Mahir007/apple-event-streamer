package discover

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeApple serves the given paths and routes every request to them,
// whatever host it was for.
func fakeApple(t *testing.T, pages map[string]string) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	return &http.Client{Transport: rewrite{target}}
}

type rewrite struct{ target *url.URL }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rw.target.Scheme, rw.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

const page = `<meta property="url-json" content="%s"><meta name="description" content="Desc">`

func TestFind(t *testing.T) {
	for _, tc := range []struct {
		name, urlJSON, json string
		want, wantErr       string
	}{
		{"absolute", "https://events.example/j.json", `{"videoSrc":{"hls":"https://events.example/m.m3u8"},"state":"vod"}`,
			"https://events.example/m.m3u8", ""},
		{"relative", "/j/url.json", `{"videoSrc":{"hls":"../m3u8/m.m3u8"}}`,
			"https://www.apple.com/m3u8/m.m3u8", ""},
		{"json points at a file", "/j.json", `{"videoSrc":{"hls":"file:///etc/passwd"}}`,
			"", "not an http(s) URL"},
		{"url-json points at a file", "file:///etc/passwd", ``, "", "not an http(s) URL"},
		{"no stream yet", "/j.json", `{"videoSrc":{"hls":""},"state":"upcoming"}`,
			"", "no stream right now (state: upcoming)"},
		{"not json", "/j.json", `<html>`, "", "decoding"},
		{"bad URL", "/j.json", `{"videoSrc":{"hls":"https://[::1"}}`, "", "stream URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonPath := "/j.json"
			if u, err := url.Parse(tc.urlJSON); err == nil && u.Scheme != "file" {
				jsonPath = u.Path
			}
			c := fakeApple(t, map[string]string{
				"/apple-events/": strings.Replace(page, "%s", tc.urlJSON, 1),
				jsonPath:         tc.json,
			})
			ev, err := Find(context.Background(), c, false)
			switch {
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			case tc.wantErr == "" && err != nil:
				t.Fatal(err)
			case tc.wantErr == "" && (ev.URL != tc.want || ev.Description != "Desc"):
				t.Fatalf("got %+v, want URL %s", ev, tc.want)
			}
		})
	}
}

func TestFetchRefusesOversizedBodies(t *testing.T) {
	c := fakeApple(t, map[string]string{"/big": strings.Repeat("x", MaxBody+1), "/ok": strings.Repeat("x", MaxBody)})
	if _, err := Fetch(context.Background(), c, "https://www.apple.com/big"); err == nil || !strings.Contains(err.Error(), "larger than 16 MiB") {
		t.Fatalf("oversized body: err = %v", err)
	}
	if b, err := Fetch(context.Background(), c, "https://www.apple.com/ok"); err != nil || len(b) != MaxBody {
		t.Fatalf("body at the limit: %d bytes, %v", len(b), err)
	}
}

func FuzzMetaContents(f *testing.F) {
	f.Add(`<meta property="url-json" content="https://x/j.json"><META NAME='description' CONTENT='a &amp; b'>`)
	f.Fuzz(func(t *testing.T, page string) {
		for k := range metaContents(page) {
			if k != strings.ToLower(k) {
				t.Fatalf("key %q is not lower-cased", k)
			}
		}
	})
}
