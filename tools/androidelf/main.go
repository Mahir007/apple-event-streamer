// Command androidelf clears the DF_1_PIE flag in Android executables, in place.
// The linker sets it, and Android 6's dynamic linker doesn't know it, so it
// prints "unsupported flags DT_FLAGS_1=0x8000001" every time the program runs.
// Android doesn't need the flag: it only runs position-independent executables.
//
//	go run ./tools/androidelf FILE...
package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: androidelf FILE...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		if err := run(path); err != nil {
			fmt.Fprintf(os.Stderr, "androidelf: %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func run(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := clearPIE(data); err != nil {
		return err
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, st.Mode())
}

// clearPIE clears DF_1_PIE in the dynamic table of a 64-bit little-endian ELF
// file. A file with no dynamic table can't be an Android executable, so it's
// an error, which stops a broken build from going unnoticed.
func clearPIE(data []byte) error {
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return errors.New("not a 64-bit little-endian ELF file")
	}
	for _, p := range f.Progs {
		if p.Type != elf.PT_DYNAMIC {
			continue
		}
		if p.Off+p.Filesz > uint64(len(data)) {
			return errors.New("the dynamic table runs past the end of the file")
		}
		const entry = 16 // a tag and a value, 8 bytes each
		table := data[p.Off : p.Off+p.Filesz]
		for ; len(table) >= entry; table = table[entry:] {
			if elf.DynTag(binary.LittleEndian.Uint64(table)) == elf.DT_FLAGS_1 {
				flags := binary.LittleEndian.Uint64(table[8:])
				binary.LittleEndian.PutUint64(table[8:], flags&^uint64(elf.DF_1_PIE))
			}
		}
		return nil
	}
	return errors.New("no dynamic table")
}
