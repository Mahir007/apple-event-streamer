package main

import (
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func TestNumericVersion(t *testing.T) {
	for v, want := range map[string][4]uint16{
		"v1.2.3":                 {1, 2, 3, 0},
		"1.2.3":                  {1, 2, 3, 0},
		"v1.10.0-rc.1":           {1, 10, 0, 0},
		"v1.2.3-4-gabcdef-dirty": {1, 2, 3, 0},
		"v1.2.3+meta":            {1, 2, 3, 0},
		"482ef6a-dirty":          {},
		"ci-482ef6a":             {},
		"dev":                    {},
		"v1.2":                   {},
		"v1.2.70000":             {},
	} {
		if got := numericVersion(v); got != want {
			t.Errorf("numericVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestLinked links the resource into real Windows executables and reads it
// back the way Windows does: through the image's resource directory.
func TestLinked(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	in := info{
		name:        "app",
		version:     "v1.2.3-4-gabcdef",
		description: "Test App ▶",
		company:     "Someone",
		copyright:   "Copyright © 2026 Someone",
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module app\n"), 0o644)
			os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
			syso, err := object(arch, versionInfo(in))
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, "rsrc_windows_"+arch+".syso"), syso, 0o644)
			exe := filepath.Join(dir, "app.exe")
			cmd := exec.Command("go", "build", "-o", exe, ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, b)
			}

			f, err := pe.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			sect := f.Section(".rsrc")
			if sect == nil {
				t.Fatal("no .rsrc section in the executable")
			}
			rsrc, err := sect.Data()
			if err != nil {
				t.Fatal(err)
			}
			data := resource(t, rsrc, sect.VirtualAddress, 16, 1, langID)
			fixed, strs := decode(t, data)

			le := binary.LittleEndian
			if sig := le.Uint32(fixed); sig != 0xfeef04bd {
				t.Errorf("fixed info signature %#x", sig)
			}
			if ms, ls := le.Uint32(fixed[8:]), le.Uint32(fixed[12:]); ms != 1<<16|2 || ls != 3<<16 {
				t.Errorf("file version %#x.%#x, want 1.2.3.0", ms, ls)
			}
			want := map[string]string{
				"CompanyName":      "Someone",
				"FileDescription":  "Test App ▶",
				"FileVersion":      "v1.2.3-4-gabcdef",
				"InternalName":     "app",
				"LegalCopyright":   "Copyright © 2026 Someone",
				"OriginalFilename": "app.exe",
				"ProductName":      "app",
				"ProductVersion":   "v1.2.3-4-gabcdef",
				"Translation":      "ЉҰ",
			}
			for k, v := range want {
				if strs[k] != v {
					t.Errorf("%s = %q, want %q", k, strs[k], v)
				}
			}
			if len(strs) != len(want) {
				t.Errorf("got %d values, want %d: %q", len(strs), len(want), strs)
			}
		})
	}
}

// resource follows type › name › language through the resource directory
// and returns the data the leaf entry points at.
func resource(t *testing.T, rsrc []byte, sectRVA uint32, ids ...uint32) []byte {
	t.Helper()
	le := binary.LittleEndian
	off := uint32(0)
	for _, id := range ids {
		named, n := le.Uint16(rsrc[off+12:]), le.Uint16(rsrc[off+14:])
		found := false
		for i := uint32(0); i < uint32(named)+uint32(n); i++ {
			e := off + 16 + 8*i
			if le.Uint32(rsrc[e:]) == id {
				off, found = le.Uint32(rsrc[e+4:])&^0x80000000, true
				break
			}
		}
		if !found {
			t.Fatalf("resource %v: no entry %d", ids, id)
		}
	}
	rva, size := le.Uint32(rsrc[off:]), le.Uint32(rsrc[off+4:])
	start := rva - sectRVA // fails here if the relocation wasn't applied
	if uint64(start)+uint64(size) > uint64(len(rsrc)) {
		t.Fatalf("data entry RVA %#x size %d is outside .rsrc (RVA %#x, %d bytes)", rva, size, sectRVA, len(rsrc))
	}
	return rsrc[start : start+size]
}

// decode walks a VS_VERSIONINFO tree, returning the fixed info and every
// String and Var value by key.
func decode(t *testing.T, b []byte) (fixed []byte, values map[string]string) {
	t.Helper()
	values = map[string]string{}
	var walk func(b []byte, depth int)
	walk = func(b []byte, depth int) {
		le := binary.LittleEndian
		length, valueLen, typ := int(le.Uint16(b)), int(le.Uint16(b[2:])), le.Uint16(b[4:])
		if length > len(b) {
			t.Fatalf("block length %d exceeds %d", length, len(b))
		}
		b = b[:length]
		var key []uint16
		pos := 6
		for ; le.Uint16(b[pos:]) != 0; pos += 2 {
			key = append(key, le.Uint16(b[pos:]))
		}
		pos = (pos + 2 + 3) &^ 3
		name := string(utf16.Decode(key))
		size := valueLen
		if typ == 1 {
			size *= 2
		}
		value := b[pos : pos+size]
		switch {
		case depth == 0:
			fixed = value
		case typ == 1 && size > 0:
			var u []uint16
			for i := 0; i+2 < len(value); i += 2 { // drop the terminator
				u = append(u, le.Uint16(value[i:]))
			}
			values[name] = string(utf16.Decode(u))
		case size > 0:
			var u []uint16
			for i := 0; i < len(value); i += 2 {
				u = append(u, le.Uint16(value[i:]))
			}
			values[name] = string(utf16.Decode(u))
		}
		for pos = (pos + size + 3) &^ 3; pos < length; {
			child := int(le.Uint16(b[pos:]))
			walk(b[pos:], depth+1)
			pos = (pos + child + 3) &^ 3
		}
	}
	walk(b, 0)
	return fixed, values
}
