// Command winres writes a COFF object (.syso) holding a Windows version
// resource. The Go linker embeds a package's .syso files, and Windows shows
// the resource's FileDescription as the process name in Task Manager and its
// fields in the executable's Properties › Details tab.
//
//	go run ./tools/winres -arch amd64 -version v1.2.3 -name app \
//		-description "App" -o rsrc_windows_amd64.syso
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

type info struct {
	name, version, description, company, copyright string
}

func main() {
	out := flag.String("o", "", "output path")
	arch := flag.String("arch", "", "amd64 or arm64")
	var in info
	flag.StringVar(&in.name, "name", "", "executable name without .exe")
	flag.StringVar(&in.version, "version", "", "version string, e.g. v1.2.3")
	flag.StringVar(&in.description, "description", "", "FileDescription (shown in Task Manager)")
	flag.StringVar(&in.company, "company", "", "CompanyName")
	flag.StringVar(&in.copyright, "copyright", "", "LegalCopyright")
	flag.Parse()
	if *out == "" || *arch == "" || in.name == "" || in.version == "" || in.description == "" || flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: winres -o OUTPUT -arch ARCH -name NAME -version VERSION -description TEXT [-company TEXT] [-copyright TEXT]")
		os.Exit(2)
	}
	syso, err := object(*arch, versionInfo(in))
	if err == nil {
		err = os.WriteFile(*out, syso, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "winres:", err)
		os.Exit(1)
	}
}

// numericVersion extracts major.minor.patch from versions like v1.2.3,
// v1.2.3-rc.1 or v1.2.3-4-gabcdef. Anything else (a bare commit hash,
// "dev") is 0.0.0.0; the string fields still carry the full version.
func numericVersion(v string) [4]uint16 {
	var out [4]uint16
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 3 {
		return out
	}
	if i := strings.IndexAny(parts[2], "-+"); i >= 0 {
		parts[2] = parts[2][:i]
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return [4]uint16{}
		}
		out[i] = uint16(n)
	}
	return out
}

// Language and code page of the string table: U.S. English, UTF-16.
const (
	langID   = 0x0409
	codePage = 0x04b0
)

// versionInfo encodes a VS_VERSIONINFO structure.
func versionInfo(in info) []byte {
	v := numericVersion(in.version)
	ms := uint32(v[0])<<16 | uint32(v[1])
	ls := uint32(v[2])<<16 | uint32(v[3])
	fixed := make([]byte, 0, 52)
	for _, d := range []uint32{
		0xfeef04bd, // signature
		0x00010000, // structure version 1.0
		ms, ls,     // file version
		ms, ls, // product version
		0x3f,       // flags mask
		0,          // flags
		0x00040004, // VOS_NT_WINDOWS32
		1,          // VFT_APP
		0,          // subtype
		0, 0,       // date
	} {
		fixed = binary.LittleEndian.AppendUint32(fixed, d)
	}

	var strs [][]byte
	for _, kv := range [][2]string{
		{"CompanyName", in.company},
		{"FileDescription", in.description},
		{"FileVersion", in.version},
		{"InternalName", in.name},
		{"LegalCopyright", in.copyright},
		{"OriginalFilename", in.name + ".exe"},
		{"ProductName", in.name},
		{"ProductVersion", in.version},
	} {
		if kv[1] != "" {
			s := utf16z(kv[1])
			strs = append(strs, block(kv[0], 1, s, uint16(len(s)/2)))
		}
	}
	table := block(fmt.Sprintf("%04X%04X", langID, codePage), 1, nil, 0, strs...)
	translation := binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, langID), codePage)
	return block("VS_VERSION_INFO", 0, fixed, uint16(len(fixed)),
		block("StringFileInfo", 1, nil, 0, table),
		block("VarFileInfo", 1, nil, 0, block("Translation", 0, translation, uint16(len(translation)))))
}

// block encodes one node of the version resource tree: a length, value
// length and type header, the NUL-terminated UTF-16 key, then the value and
// children, each aligned to 4 bytes. valueLen is in bytes for binary values
// (typ 0) and in UTF-16 units for text (typ 1).
func block(key string, typ uint16, value []byte, valueLen uint16, children ...[]byte) []byte {
	b := append(make([]byte, 6), utf16z(key)...)
	b = pad4(b)
	b = append(b, value...)
	for _, c := range children {
		b = append(pad4(b), c...)
	}
	binary.LittleEndian.PutUint16(b[0:], uint16(len(b)))
	binary.LittleEndian.PutUint16(b[2:], valueLen)
	binary.LittleEndian.PutUint16(b[4:], typ)
	return b
}

func utf16z(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return append(b, 0, 0)
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// object wraps a version resource in a COFF object with a single .rsrc
// section: a three-level resource directory (type RT_VERSION › ID 1 ›
// language) leading to one data entry. The data entry's address is an
// image-relative relocation against the section, which the linker resolves.
func object(arch string, data []byte) ([]byte, error) {
	var machine, relocType uint16
	switch arch {
	case "amd64":
		machine, relocType = 0x8664, 0x0003 // IMAGE_REL_AMD64_ADDR32NB
	case "arm64":
		machine, relocType = 0xaa64, 0x0002 // IMAGE_REL_ARM64_ADDR32NB
	default:
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}

	const (
		rtVersion   = 16
		subdir      = 0x80000000
		dirSize     = 16 + 8 // IMAGE_RESOURCE_DIRECTORY with one entry
		dataEntry   = 3 * dirSize
		dataOffset  = dataEntry + 16
		headersSize = 20 + 40 // file header + one section header
	)
	le := binary.LittleEndian
	var rsrc []byte
	dir := func(id, target uint32) {
		rsrc = append(rsrc, make([]byte, 14)...) // characteristics, timestamp, versions, named entries
		rsrc = le.AppendUint16(rsrc, 1)          // ID entries
		rsrc = le.AppendUint32(rsrc, id)
		rsrc = le.AppendUint32(rsrc, target)
	}
	dir(rtVersion, subdir|dirSize)
	dir(1, subdir|2*dirSize)
	dir(langID, dataEntry)
	rsrc = le.AppendUint32(rsrc, dataOffset) // address, relocated
	rsrc = le.AppendUint32(rsrc, uint32(len(data)))
	rsrc = append(rsrc, make([]byte, 8)...) // code page, reserved
	rsrc = pad4(append(rsrc, data...))

	relocs := headersSize + uint32(len(rsrc))
	symbols := relocs + 10

	b := le.AppendUint16(nil, machine)
	b = le.AppendUint16(b, 1) // sections
	b = le.AppendUint32(b, 0) // timestamp
	b = le.AppendUint32(b, symbols)
	b = le.AppendUint32(b, 1) // symbols
	b = le.AppendUint16(b, 0) // optional header size
	b = le.AppendUint16(b, 0) // characteristics

	b = append(b, ".rsrc\x00\x00\x00"...)
	b = le.AppendUint32(b, 0) // virtual size
	b = le.AppendUint32(b, 0) // virtual address
	b = le.AppendUint32(b, uint32(len(rsrc)))
	b = le.AppendUint32(b, headersSize)
	b = le.AppendUint32(b, relocs)
	b = le.AppendUint32(b, 0)          // line numbers
	b = le.AppendUint16(b, 1)          // relocations
	b = le.AppendUint16(b, 0)          // line numbers
	b = le.AppendUint32(b, 0x40000040) // IMAGE_SCN_CNT_INITIALIZED_DATA | IMAGE_SCN_MEM_READ

	b = append(b, rsrc...)

	b = le.AppendUint32(b, dataEntry) // offset of the data entry's address
	b = le.AppendUint32(b, 0)         // symbol 0: the section
	b = le.AppendUint16(b, relocType)

	b = append(b, ".rsrc\x00\x00\x00"...)
	b = le.AppendUint32(b, 0) // value
	b = le.AppendUint16(b, 1) // section number
	b = le.AppendUint16(b, 0) // type
	b = append(b, 3, 0)       // IMAGE_SYM_CLASS_STATIC, no aux symbols

	return le.AppendUint32(b, 4), nil // empty string table
}
