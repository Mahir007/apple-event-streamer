package discover

import "testing"

func TestMetaContents(t *testing.T) {
	page := `<head>
	<meta property="url-json" content="https://events-delivery.apple.com/abc/json/url.json">
	<meta name="Description" content="Introducing the iPhone Duo &amp; more." />
	<meta content='x' property='og:title'>
	</head>`
	m := metaContents(page)
	if got := m["url-json"]; got != "https://events-delivery.apple.com/abc/json/url.json" {
		t.Errorf("url-json = %q", got)
	}
	if got := m["description"]; got != "Introducing the iPhone Duo & more." {
		t.Errorf("description = %q", got)
	}
	if got := m["og:title"]; got != "x" {
		t.Errorf("og:title = %q", got)
	}
}
