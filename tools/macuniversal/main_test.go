package main

import (
	"bytes"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUniversal(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	os.WriteFile(src, []byte("package main\nfunc main() {}\n"), 0o644)
	var thin []string
	for _, arch := range []string{"arm64", "amd64"} {
		out := filepath.Join(dir, arch)
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Env = append(os.Environ(), "GOOS=darwin", "GOARCH="+arch, "CGO_ENABLED=0")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", arch, err, b)
		}
		thin = append(thin, out)
	}

	fat := filepath.Join(dir, "fat")
	if err := run(fat, thin); err != nil {
		t.Fatal(err)
	}
	ff, err := macho.OpenFat(fat)
	if err != nil {
		t.Fatal(err)
	}
	defer ff.Close()
	if len(ff.Arches) != 2 || ff.Arches[0].Cpu != macho.CpuAmd64 || ff.Arches[1].Cpu != macho.CpuArm64 {
		t.Fatalf("arches = %+v", ff.Arches)
	}
	raw, _ := os.ReadFile(fat)
	for _, a := range ff.Arches {
		if a.Offset%(1<<a.Align) != 0 {
			t.Errorf("%v slice misaligned at %d", a.Cpu, a.Offset)
		}
		want, _ := os.ReadFile(filepath.Join(dir, map[macho.Cpu]string{macho.CpuAmd64: "amd64", macho.CpuArm64: "arm64"}[a.Cpu]))
		got := raw[a.Offset : a.Offset+a.Size]
		if !bytes.Equal(got, want) {
			t.Errorf("%v slice differs from its input", a.Cpu)
		}
	}
	if err := run(fat, []string{thin[0], thin[0]}); err == nil {
		t.Error("duplicate architectures should fail")
	}
}
