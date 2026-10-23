// Command macuniversal merges thin Mach-O executables into a universal
// ("fat") binary, like `lipo -create`, so universal2 macOS builds can be
// made on any OS.
//
//	go run ./tools/macuniversal -o out/app app-amd64 app-arm64
package main

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

type slice struct {
	path   string
	cpu    macho.Cpu
	subcpu uint32
	data   []byte
	align  uint32 // log2
}

func main() {
	out := flag.String("o", "", "output path")
	flag.Parse()
	if *out == "" || flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: macuniversal -o OUTPUT THIN1 THIN2 [...]")
		os.Exit(2)
	}
	if err := run(*out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "macuniversal:", err)
		os.Exit(1)
	}
}

func run(out string, inputs []string) error {
	var slices []slice
	seen := map[macho.Cpu]string{}
	for _, p := range inputs {
		s, err := load(p)
		if err != nil {
			return err
		}
		if prev, dup := seen[s.cpu]; dup {
			return fmt.Errorf("%s and %s are both %v", prev, p, s.cpu)
		}
		seen[s.cpu] = p
		slices = append(slices, s)
	}
	// Same order as lipo: x86_64 before arm64.
	sort.Slice(slices, func(i, j int) bool { return slices[i].cpu < slices[j].cpu })

	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if err := write(f, slices); err != nil {
		f.Close()
		os.Remove(out)
		return err
	}
	return f.Close()
}

func load(path string) (slice, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return slice{}, err
	}
	f, err := macho.NewFile(bytes.NewReader(data))
	if err != nil {
		return slice{}, fmt.Errorf("%s: %w", path, err)
	}
	defer f.Close()
	s := slice{path: path, cpu: f.Cpu, subcpu: f.SubCpu, data: data}
	switch f.Cpu {
	case macho.CpuAmd64, macho.Cpu386:
		s.align = 12 // 4 KiB pages
	case macho.CpuArm64, macho.CpuArm:
		s.align = 14 // 16 KiB pages
	default:
		return slice{}, fmt.Errorf("%s: unsupported CPU %v", path, f.Cpu)
	}
	return s, nil
}

// write emits a fat_header followed by fat_arch records (all big-endian)
// and the page-aligned slices.
func write(w io.Writer, slices []slice) error {
	const fatMagic = 0xcafebabe
	header := []uint32{fatMagic, uint32(len(slices))}
	offset := uint64(8 + 20*len(slices))
	offsets := make([]uint64, len(slices))
	for i, s := range slices {
		a := uint64(1) << s.align
		offset = (offset + a - 1) &^ (a - 1)
		offsets[i] = offset
		offset += uint64(len(s.data))
		header = append(header, uint32(s.cpu), s.subcpu, uint32(offsets[i]), uint32(len(s.data)), s.align)
	}
	if offset > 1<<32-1 {
		return errors.New("universal binary would exceed 4 GiB")
	}
	if err := binary.Write(w, binary.BigEndian, header); err != nil {
		return err
	}
	pos := uint64(4 * len(header))
	for i, s := range slices {
		if _, err := w.Write(make([]byte, offsets[i]-pos)); err != nil {
			return err
		}
		if _, err := w.Write(s.data); err != nil {
			return err
		}
		pos = offsets[i] + uint64(len(s.data))
	}
	return nil
}
