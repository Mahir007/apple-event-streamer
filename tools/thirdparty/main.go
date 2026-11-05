// Command thirdparty writes the licence notices of everything linked into
// the release binaries: every non-main module compiled into any target
// (found with `go list -deps`) plus the Go standard library and the Android
// NDK's C startup code.
//
//	go run ./tools/thirdparty -o THIRD_PARTY_LICENSES
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// targets must cover every platform a release binary is built for, since
// some modules (e.g. x/sys/windows, termios) are only linked on some.
// android/amd64 links the same modules as android/arm64, but go list can't
// load it without cgo.
var targets = []string{"linux/amd64", "linux/arm64", "android/arm64", "windows/amd64", "windows/arm64", "darwin/amd64", "darwin/arm64"}

var licenseFile = regexp.MustCompile(`(?i)^(LICEN[CS]E|COPYING|NOTICE|PATENTS)([.-].*)?$`)

type module struct {
	path, version, dir string
	files              []string
	text               string // the notice itself, for code that isn't a module
}

// ndkStartup is the notice for crtbegin_dynamic.o and crtend_android.o, which
// the NDK links into Android binaries. Its other static code, compiler-rt and
// libunwind, is under the LLVM exception, which needs no notice in binaries.
var ndkStartup = &module{path: "Android NDK C startup code", version: "r29", text: `Copyright (C) 2008 The Android Open Source Project
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions
are met:
 * Redistributions of source code must retain the above copyright
   notice, this list of conditions and the following disclaimer.
 * Redistributions in binary form must reproduce the above copyright
   notice, this list of conditions and the following disclaimer in
   the documentation and/or other materials provided with the
   distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS
FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE
COPYRIGHT OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT,
INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING,
BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS
OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED
AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT
OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF
SUCH DAMAGE.`}

func main() {
	out := flag.String("o", "THIRD_PARTY_LICENSES", "output file")
	pkg := flag.String("pkg", ".", "main package")
	flag.Parse()
	if err := run(*out, *pkg); err != nil {
		fmt.Fprintln(os.Stderr, "thirdparty:", err)
		os.Exit(1)
	}
}

func run(out, pkg string) error {
	mods := map[string]*module{}
	for _, t := range targets {
		goos, goarch, _ := strings.Cut(t, "/")
		cmd := exec.Command("go", "list", "-deps", "-f",
			`{{if not .Standard}}{{with .Module}}{{if not .Main}}{{with .Replace}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{else}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}{{end}}{{end}}`, pkg)
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
		cmd.Stderr = os.Stderr
		b, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("go list for %s: %w", t, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			if len(f) == 3 && mods[f[0]] == nil {
				mods[f[0]] = &module{path: f[0], version: f[1], dir: f[2]}
			}
		}
	}

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	std := &module{path: "Go standard library", version: runtime.Version(), dir: strings.TrimSpace(string(goroot))}
	if v, err := exec.Command("go", "env", "GOVERSION").Output(); err == nil {
		std.version = strings.TrimSpace(string(v))
	}

	list := []*module{std, ndkStartup}
	var paths []string
	for p := range mods {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		list = append(list, mods[p])
	}

	for _, m := range list {
		if m.text != "" {
			continue
		}
		entries, err := os.ReadDir(m.dir)
		if err != nil {
			return fmt.Errorf("%s: %w", m.path, err)
		}
		for _, e := range entries {
			if !e.IsDir() && licenseFile.MatchString(e.Name()) {
				m.files = append(m.files, e.Name())
			}
		}
		if len(m.files) == 0 {
			return fmt.Errorf("%s %s: no licence file found in %s", m.path, m.version, m.dir)
		}
	}

	var b strings.Builder
	b.WriteString("Third-party software notices\n\n")
	b.WriteString("apple-event-streamer is licensed under the GNU GPL v3 (see LICENSE). The\n")
	b.WriteString("release binaries also contain the following software, distributed under\n")
	b.WriteString("the licences reproduced below.\n\n")
	pw, vw := 0, 0
	for _, m := range list {
		pw, vw = max(pw, len(m.path)), max(vw, len(m.version))
	}
	for _, m := range list {
		fmt.Fprintf(&b, "  %-*s  %-*s  %s\n", pw, m.path, vw, m.version, kind(m))
	}
	for _, m := range list {
		if m.text != "" {
			fmt.Fprintf(&b, "\n%s\n%s %s\n%s\n\n%s\n", strings.Repeat("=", 78), m.path, m.version, strings.Repeat("=", 78), m.text)
		}
		for _, f := range m.files {
			text, err := os.ReadFile(filepath.Join(m.dir, f))
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "\n%s\n%s %s — %s\n%s\n\n%s\n", strings.Repeat("=", 78), m.path, m.version, f, strings.Repeat("=", 78),
				strings.TrimSpace(strings.ReplaceAll(string(text), "\r\n", "\n")))
		}
	}
	return os.WriteFile(out, []byte(b.String()), 0o644)
}

// kind is a best-effort SPDX identifier for the summary table.
func kind(m *module) string {
	all := m.text
	for _, f := range m.files {
		b, _ := os.ReadFile(filepath.Join(m.dir, f))
		all += string(b)
	}
	switch {
	case strings.Contains(all, "Permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(all, "Apache License") && strings.Contains(all, "Version 2.0"):
		return "Apache-2.0"
	case strings.Contains(all, "Mozilla Public License"):
		return "MPL-2.0"
	case strings.Contains(all, "Redistribution and use in source and binary forms"):
		if strings.Contains(all, "Neither the name") || strings.Contains(all, "names of its contributors") {
			return "BSD-3-Clause"
		}
		return "BSD-2-Clause"
	case strings.Contains(all, "Permission to use, copy, modify, and/or distribute"):
		return "ISC"
	}
	return "see below"
}
