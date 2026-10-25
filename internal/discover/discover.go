// Package discover finds the current Apple Event's master playlist.
//
// https://www.apple.com/apple-events/ carries a
// <meta property="url-json" content="..."> tag pointing at a small JSON
// document whose videoSrc.hls field is the master playlist of the current
// event, both while it is live and once it becomes a replay.
package discover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// EventsPage is the Apple Events landing page.
const EventsPage = "https://www.apple.com/apple-events/"

// UserAgent is sent with every request; Apple serves the same content to
// browsers and tools, but some CDNs are picky about empty agents.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"

// MaxBody is the most Fetch and ReadLimited accept. Apple's pages and
// playlists are well under 1 MiB.
const MaxBody = 16 << 20

// Event describes the stream advertised by the events page.
type Event struct {
	// URL is the master playlist URL.
	URL string
	// State is the page's own state string, e.g. "live" or "vod".
	State string
	// Description is the page's meta description (event summary).
	Description string
}

type urlJSON struct {
	VideoSrc struct {
		HLS    string `json:"hls"`
		HLSASL string `json:"hlsASL"`
	} `json:"videoSrc"`
	State string `json:"state"`
}

var metaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
var tagAttr = regexp.MustCompile(`(?is)([a-z:-]+)\s*=\s*("[^"]*"|'[^']*')`)

// Find fetches the events page and resolves the current master playlist.
// When asl is true the American Sign Language version is returned.
func Find(ctx context.Context, client *http.Client, asl bool) (*Event, error) {
	page, err := Fetch(ctx, client, EventsPage)
	if err != nil {
		return nil, err
	}
	meta := metaContents(string(page))
	if meta["url-json"] == "" {
		return nil, errors.New("could not find the event stream on " + EventsPage + " (no url-json meta tag); pass --url instead")
	}
	jsonURL, err := resolveHTTP(EventsPage, meta["url-json"])
	if err != nil {
		return nil, fmt.Errorf("%s: url-json: %w", EventsPage, err)
	}

	body, err := Fetch(ctx, client, jsonURL)
	if err != nil {
		return nil, err
	}
	var doc urlJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", jsonURL, err)
	}

	ev := &Event{URL: doc.VideoSrc.HLS, State: doc.State, Description: meta["description"]}
	if asl {
		ev.URL = doc.VideoSrc.HLSASL
	}
	if ev.URL == "" {
		kind := "stream"
		if asl {
			kind = "sign-language stream"
		}
		state := doc.State
		if state == "" {
			state = "unknown"
		}
		return nil, fmt.Errorf("the events page has no %s right now (state: %s); try again closer to the event or pass --url", kind, state)
	}
	if ev.URL, err = resolveHTTP(jsonURL, ev.URL); err != nil {
		return nil, fmt.Errorf("%s: stream URL: %w", jsonURL, err)
	}
	return ev, nil
}

// resolveHTTP resolves ref against base and requires an http(s) result, so
// a page can't point the app at a local file or another scheme.
func resolveHTTP(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	u = b.ResolveReference(u)
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", ref)
	}
	return u.String(), nil
}

// metaContents maps meta tag names/properties (lower-cased) to content.
func metaContents(page string) map[string]string {
	out := map[string]string{}
	for _, tag := range metaTag.FindAllString(page, -1) {
		attrs := map[string]string{}
		for _, m := range tagAttr.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = html.UnescapeString(m[2][1 : len(m[2])-1])
		}
		key := attrs["property"]
		if key == "" {
			key = attrs["name"]
		}
		if key = strings.ToLower(key); key != "" {
			if _, dup := out[key]; !dup {
				out[key] = strings.TrimSpace(attrs["content"])
			}
		}
	}
	return out
}

// Fetch GETs url and returns the body, failing on non-2xx responses.
func Fetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	body, err := ReadLimited(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	return body, nil
}

// ReadLimited reads r to the end, failing if it holds more than MaxBody
// bytes rather than silently using a truncated prefix.
func ReadLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBody+1))
	if err == nil && len(b) > MaxBody {
		return nil, fmt.Errorf("larger than %d MiB", MaxBody>>20)
	}
	return b, err
}
