package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// minimalELF returns a 64-bit ELF file holding a program header of type ptype
// over a dynamic table with these tag and value pairs.
func minimalELF(t *testing.T, ptype elf.ProgType, dyn ...uint64) []byte {
	t.Helper()
	const headers = 64 + 56
	var b bytes.Buffer
	w := func(v any) {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	w(elf.Header64{
		Ident:     [elf.EI_NIDENT]byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)},
		Type:      uint16(elf.ET_DYN),
		Machine:   uint16(elf.EM_AARCH64),
		Version:   uint32(elf.EV_CURRENT),
		Phoff:     64,
		Ehsize:    64,
		Phentsize: 56,
		Phnum:     1,
	})
	size := uint64(8 * len(dyn))
	w(elf.Prog64{Type: uint32(ptype), Off: headers, Filesz: size, Memsz: size})
	w(dyn)
	return b.Bytes()
}

func dynFlags(t *testing.T, data []byte) uint64 {
	t.Helper()
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	table := data[f.Progs[0].Off:]
	for ; len(table) >= 16; table = table[16:] {
		if elf.DynTag(binary.LittleEndian.Uint64(table)) == elf.DT_FLAGS_1 {
			return binary.LittleEndian.Uint64(table[8:])
		}
	}
	t.Fatal("no DT_FLAGS_1")
	return 0
}

func TestClearPIE(t *testing.T) {
	const now, other = uint64(elf.DF_1_NOW), uint64(5)
	data := minimalELF(t, elf.PT_DYNAMIC,
		uint64(elf.DT_NEEDED), other,
		uint64(elf.DT_FLAGS_1), now|uint64(elf.DF_1_PIE),
		uint64(elf.DT_NULL), 0)
	if err := clearPIE(data); err != nil {
		t.Fatal(err)
	}
	if got := dynFlags(t, data); got != now {
		t.Errorf("flags = %#x, want %#x", got, now)
	}
	if got := binary.LittleEndian.Uint64(data[64+56+8:]); got != other {
		t.Errorf("another entry changed to %d", got)
	}
}

func TestClearPIEWithoutFlags(t *testing.T) {
	data := minimalELF(t, elf.PT_DYNAMIC, uint64(elf.DT_NULL), 0)
	want := bytes.Clone(data)
	if err := clearPIE(data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Error("the file changed")
	}
}

func TestClearPIEErrors(t *testing.T) {
	if err := clearPIE([]byte("not an ELF file")); err == nil {
		t.Error("accepted a file that isn't ELF")
	}
	if err := clearPIE(minimalELF(t, elf.PT_LOAD, 0, 0)); err == nil {
		t.Error("accepted a file with no dynamic table")
	}
	data := minimalELF(t, elf.PT_DYNAMIC, uint64(elf.DT_NULL), 0)
	if err := clearPIE(data[:len(data)-1]); err == nil {
		t.Error("accepted a truncated dynamic table")
	}
}

func TestRunKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app")
	data := minimalELF(t, elf.PT_DYNAMIC, uint64(elf.DT_FLAGS_1), uint64(elf.DF_1_PIE), uint64(elf.DT_NULL), 0)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if flags := dynFlags(t, got); flags != 0 {
		t.Errorf("flags = %#x", flags)
	}
	if st, _ := os.Stat(path); st.Mode() != mustMode(t, 0o755) {
		t.Errorf("mode = %v", st.Mode())
	}
}

// mustMode returns what a new file with this mode gets on this system, which
// differs on Windows.
func mustMode(t *testing.T, m os.FileMode) os.FileMode {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ref")
	if err := os.WriteFile(p, nil, m); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode()
}
