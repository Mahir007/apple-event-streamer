package sanitize

import (
	"testing"
	"unicode/utf8"
)

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"Dolby Atmos · 2160p (4K)": "Dolby Atmos · 2160p (4K)",
		"日本語 👩‍👩‍👧 é":              "日本語 👩‍👩‍👧 é",
		"a\x1b]0;title\x07b":       "a�]0;title�b",
		"\x1b[2J\x1b[31mred":       "�[2J�[31mred",
		"tab\tnew\nline\r":         "tab�new�line�",
		"c1 \u009b31m":             "c1 �31m",
		"bad \xff\xfe utf8":        "bad � utf8",
		"rtl \u202egnp.exe":        "rtl �gnp.exe",
		"del\x7f":                  "del�",
		"existing � stays":         "existing � stays",
	} {
		if got := Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func FuzzText(f *testing.F) {
	f.Add("a\x1b[31mb\xff\u202e")
	f.Fuzz(func(t *testing.T, s string) {
		out := Text(s)
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8: %q", out)
		}
		for _, r := range out {
			if bad(r) {
				t.Fatalf("%q survived in %q", r, out)
			}
		}
		if Text(out) != out {
			t.Fatalf("not idempotent: %q", out)
		}
	})
}
