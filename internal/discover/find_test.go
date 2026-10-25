package discover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeApple serves the given paths and routes every request to them,
// whatever host it was for.
func fakeApple(t *testing.T, pages map[string]string) *Fetcher {
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
	return &Fetcher{Client: &http.Client{Transport: rewrite{target}, Timeout: 5 * time.Second}, retryDelay: time.Millisecond}
}

type rewrite struct{ target *url.URL }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rw.target.Scheme, rw.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// realPage and realJSON are www.apple.com/apple-events/ and its url-json
// document as served in October 2026, after the event (state "vod").
// TODO: add a copy of both captured while an event is live; the page can
// only be saved during one.
func realPage(t *testing.T) (page, jsonPath, jsonBody string) {
	t.Helper()
	p, err := os.ReadFile("testdata/vod-page.html")
	if err != nil {
		t.Fatal(err)
	}
	j, err := os.ReadFile("testdata/vod-url.json")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(metaContents(string(p))["url-json"])
	if err != nil || u.Path == "" {
		t.Fatalf("fixture has no usable url-json tag: %v", err)
	}
	return string(p), u.Path, string(j)
}

func TestFindRealPage(t *testing.T) {
	page, jsonPath, body := realPage(t)
	var doc struct {
		VideoSrc struct{ HLS, HLSASL string } `json:"videoSrc"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	f := fakeApple(t, map[string]string{"/apple-events/": page, jsonPath: body})
	for asl, want := range map[bool]string{false: doc.VideoSrc.HLS, true: doc.VideoSrc.HLSASL} {
		ev, err := f.Find(context.Background(), asl)
		if err != nil {
			t.Fatalf("asl=%v: %v", asl, err)
		}
		if ev.URL != want || ev.State != "vod" || !strings.Contains(ev.Description, "iPhone") {
			t.Errorf("asl=%v: got %+v, want URL %s, state vod and the page description", asl, ev, want)
		}
	}
}

// TestFindChangedFormat simulates Apple changing the page or the JSON:
// every case must end in an error that says so, never a panic or a
// misleading "no stream yet".
func TestFindChangedFormat(t *testing.T) {
	page, jsonPath, body := realPage(t)
	noTag := regexp.MustCompile(`(?i)<meta[^>]*url-json[^>]*>`).ReplaceAllString(page, "")
	for _, tc := range []struct {
		name, page, json string
		wantErr          string
	}{
		{"page without url-json tag", noTag, body, "no url-json tag); Apple may have changed the page"},
		{"empty page", "", body, "no url-json tag"},
		{"videoSrc is a string", page, `{"videoSrc":"https://x/m.m3u8","state":"vod"}`, "aren't in the expected format"},
		{"hls is a number", page, `{"videoSrc":{"hls":5},"state":"vod"}`, "aren't in the expected format"},
		{"videoSrc is a list", page, `{"videoSrc":[],"state":"vod"}`, "aren't in the expected format"},
		{"keys renamed", page, `{"video":{"hls":"https://x/m.m3u8"},"status":"vod"}`, "list no stream and no state"},
		{"empty object", page, `{}`, "list no stream and no state"},
		{"null", page, `null`, "list no stream and no state"},
		{"empty body", page, ``, "aren't in the expected format"},
		{"hls renamed while live", page, `{"videoSrc":{"hlsUrl":"https://x/m.m3u8"},"state":"live"}`, `say "live" but list no stream`},
		{"html instead of json", page, `<html>`, "aren't in the expected format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fakeApple(t, map[string]string{"/apple-events/": tc.page, jsonPath: tc.json})
			ev, err := f.Find(context.Background(), false)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %+v, %v; want an error containing %q", ev, err, tc.wantErr)
			}
		})
	}
}

func TestFind(t *testing.T) {
	const page = `<meta property="url-json" content="%s"><meta name="description" content="Desc">`
	for _, tc := range []struct {
		name, urlJSON, json string
		asl                 bool
		want, wantErr       string
	}{
		{"absolute", "https://events.example/j.json", `{"videoSrc":{"hls":"https://events.example/m.m3u8"},"state":"vod"}`,
			false, "https://events.example/m.m3u8", ""},
		{"relative", "/j/url.json", `{"videoSrc":{"hls":"../m3u8/m.m3u8"}}`,
			false, "https://www.apple.com/m3u8/m.m3u8", ""},
		{"json points at a file", "/j.json", `{"videoSrc":{"hls":"file:///etc/passwd"}}`,
			false, "", "not an http(s) URL"},
		{"url-json points at a file", "file:///etc/passwd", ``, false, "", "not an http(s) URL"},
		{"no stream yet", "/j.json", `{"videoSrc":{"hls":""},"state":"upcoming"}`,
			false, "", "no stream right now (state: upcoming)"},
		{"no ASL version", "/j.json", `{"videoSrc":{"hls":"https://x/m.m3u8"},"state":"vod"}`,
			true, "", "no sign-language stream right now (state: vod)"},
		{"bad URL", "/j.json", `{"videoSrc":{"hls":"https://[::1"}}`, false, "", "unusable stream address"},
		{"json missing", "/missing.json", ``, false, "", "couldn't load the event details from www.apple.com: the server answered 404 Not Found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := map[string]string{"/apple-events/": strings.Replace(page, "%s", tc.urlJSON, 1)}
			if u, err := url.Parse(tc.urlJSON); err == nil && u.Scheme != "file" && tc.json != "" {
				pages[u.Path] = tc.json
			}
			ev, err := fakeApple(t, pages).Find(context.Background(), tc.asl)
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
