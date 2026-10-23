package shortid

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := New()
		if len(id) != Length {
			t.Fatalf("len(%q) = %d", id, len(id))
		}
		if strings.Trim(id, alphabet) != "" {
			t.Fatalf("%q has characters outside the alphabet", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
}

func TestEncodeMax(t *testing.T) {
	var u [16]byte
	for i := range u {
		u[i] = 0xff
	}
	if got := Encode(u); len(got) != Length {
		t.Fatalf("max UUID encodes to %d chars", len(got))
	}
	if got := Encode([16]byte{}); got != strings.Repeat("2", Length) {
		t.Fatalf("zero UUID = %q", got)
	}
}
