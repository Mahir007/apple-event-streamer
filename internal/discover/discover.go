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
	"net/url"
	"regexp"
	"strings"
)

// EventsPage is the Apple Events landing page.
const EventsPage = "https://www.apple.com/apple-events/"

// UserAgent is sent with every request; Apple serves the same content to
// browsers and tools, but some CDNs are picky about empty agents.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"

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
	VideoSrc *struct {
		HLS    string `json:"hls"`
		HLSASL string `json:"hlsASL"`
	} `json:"videoSrc"`
	State string `json:"state"`
}

// formatHint ends errors about pages that don't look the way Apple's do.
const formatHint = "Apple may have changed the page; pass --url instead"

var metaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
var tagAttr = regexp.MustCompile(`(?is)([a-z:-]+)\s*=\s*("[^"]*"|'[^']*')`)

// Find fetches the events page and resolves the current master playlist.
// When asl is true the American Sign Language version is returned.
func (f *Fetcher) Find(ctx context.Context, asl bool) (*Event, error) {
	page, err := f.Fetch(ctx, "the Apple Events page", EventsPage)
	if err != nil {
		return nil, err
	}
	meta := metaContents(string(page))
	if meta["url-json"] == "" {
		return nil, errors.New("the Apple Events page doesn't say where the event stream is (no url-json tag); " + formatHint)
	}
	jsonURL, err := resolveHTTP(EventsPage, meta["url-json"])
	if err != nil {
		return nil, fmt.Errorf("the Apple Events page points at an unusable address (%w); %s", err, formatHint)
	}

	body, err := f.Fetch(ctx, "the event details", jsonURL)
	if err != nil {
		return nil, err
	}
	var doc urlJSON
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("the event details aren't in the expected format (%w); %s", err, formatHint)
	}
	if doc.VideoSrc == nil && doc.State == "" {
		return nil, errors.New("the event details list no stream and no state; " + formatHint)
	}

	ev := &Event{State: doc.State, Description: meta["description"]}
	if doc.VideoSrc != nil {
		ev.URL = doc.VideoSrc.HLS
		if asl {
			ev.URL = doc.VideoSrc.HLSASL
		}
	}
	if ev.URL == "" {
		kind := "stream"
		if asl {
			kind = "sign-language stream"
		}
		switch {
		case !asl && (doc.State == "live" || doc.State == "vod"): // an event may lack an ASL version
			return nil, fmt.Errorf("the event details say %q but list no %s; %s", doc.State, kind, formatHint)
		case doc.State == "":
			doc.State = "unknown"
		}
		return nil, fmt.Errorf("the events page has no %s right now (state: %s); try again closer to the event or pass --url", kind, doc.State)
	}
	if ev.URL, err = resolveHTTP(jsonURL, ev.URL); err != nil {
		return nil, fmt.Errorf("the event details point at an unusable stream address (%w); %s", err, formatHint)
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
