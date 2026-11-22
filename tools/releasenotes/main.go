// Command releasenotes prints the notes for a GitHub release as Markdown: the
// tag's message, if it says more than the tag's name, a download table, the
// commits since the previous release grouped by their Conventional Commits
// type, and the archives' checksums. Run it in the repository.
//
// A pre-release lists the commits since the tag before it, of either kind. A
// final release lists everything since the previous final release, so its
// notes include its release candidates'.
//
//	go run ./tools/releasenotes TAG OWNER/REPO SHA256FILE...
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const name = "apple-event-streamer"

var (
	tagPattern     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)
	subjectPattern = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^)]+)\))?(!)?: (.+)$`)
	breakingLine   = regexp.MustCompile(`^BREAKING[ -]CHANGE: (.*)$`)
	footerLine     = regexp.MustCompile(`^[A-Za-z-]+(?:: | #)`)
	plainWord      = regexp.MustCompile(`^[a-z]+('[a-z]+)?(\s|$)`)
	// git revert names the reverted commit in the body and quotes its subject
	// in the revert's subject, which may be reworded to revert: <subject>.
	revertsLine   = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)
	revertSubject = regexp.MustCompile(`^Revert "(.+)"$`)
)

// The sections with a heading, in order. Every other commit goes in a
// collapsed one.
var sections = []struct{ key, title string }{
	{"breaking", "⚠️ Breaking changes"},
	{"feat", "✨ Features"},
	{"fix", "🐛 Fixes"},
	{"perf", "⚡ Performance"},
	{"docs", "📖 Documentation"},
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: releasenotes TAG OWNER/REPO SHA256FILE...")
		os.Exit(2)
	}
	tag, repo := os.Args[1], os.Args[2]
	if !tagPattern.MatchString(tag) {
		fmt.Fprintf(os.Stderr, "releasenotes: %s isn't a version tag, such as v1.0.0 or v1.0.0-rc.1\n", tag)
		os.Exit(2)
	}
	var sums strings.Builder
	for _, path := range os.Args[3:] {
		data, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		sums.Write(data)
	}
	message, err := tagMessage(".", tag)
	if err != nil {
		fail(err)
	}
	previous := previousTag(".", tag)
	log, err := commits(".", tag, previous)
	if err != nil {
		fail(err)
	}
	fmt.Print(notes(tag, repo, previous, message, log, sums.String()))
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "releasenotes: %v\n", err)
	os.Exit(1)
}

// git runs git in dir and returns what it prints.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// previousTag returns the tag the release is compared with, or "" for the
// first release.
func previousTag(dir, tag string) string {
	args := []string{"describe", "--tags", "--abbrev=0", "--match", "v*"}
	if !strings.Contains(tag, "-") {
		args = append(args, "--exclude", "v*-*")
	}
	out, err := git(dir, append(args, tag+"^")...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// tagMessage returns an annotated tag's message without its signature, and
// without its first line when that's only the tag's name. A lightweight tag
// has none: for-each-ref would give the tagged commit's message instead.
func tagMessage(dir, tag string) (string, error) {
	out, err := git(dir, "for-each-ref", "--format=%(objecttype)%00%(contents)", "refs/tags/"+tag)
	if err != nil {
		return "", err
	}
	kind, contents, _ := strings.Cut(out, "\x00")
	if kind != "tag" {
		return "", nil
	}
	lines := strings.Split(contents, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "-----BEGIN ") {
			lines = lines[:i]
			break
		}
	}
	if len(lines) > 0 && lines[0] == tag {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// A commit split into its Conventional Commits parts. A subject that doesn't
// follow them has no type.
type commit struct {
	sha, subject             string
	typ, scope, description  string
	breaking                 string // the entry for a breaking change
	revertsSHA, revertsTitle string // what a revert reverts
}

func parseCommit(sha, subject, body string) commit {
	c := commit{sha: sha, subject: subject, description: subject}
	if m := subjectPattern.FindStringSubmatch(subject); m != nil {
		c.typ, c.scope, c.description = strings.ToLower(m[1]), m[2], m[4]
		if text := breakingFooter(body); text != "" {
			c.breaking = sentence(c.description) + ". " + text
		} else if m[3] != "" {
			c.breaking = c.description
		}
	}
	if m := revertsLine.FindStringSubmatch(body); m != nil {
		c.revertsSHA = m[1]
	}
	if m := revertSubject.FindStringSubmatch(subject); m != nil {
		c.revertsTitle = m[1]
	} else if c.typ == "revert" {
		c.revertsTitle = c.description
	}
	return c
}

// breakingFooter returns the text of body's BREAKING CHANGE footer on one
// line. The footer runs to a blank line or the next footer, such as
// Co-Authored-By.
func breakingFooter(body string) string {
	var words []string
	found := false
	for line := range strings.Lines(strings.ReplaceAll(body, "\r\n", "\n")) {
		line = strings.TrimSuffix(line, "\n")
		if !found {
			if m := breakingLine.FindStringSubmatch(line); m != nil {
				found = true
				words = strings.Fields(m[1])
			}
			continue
		}
		if strings.TrimSpace(line) == "" || footerLine.MatchString(line) {
			break
		}
		words = append(words, strings.Fields(line)...)
	}
	return strings.Join(words, " ")
}

// sentence capitalizes s's first word if it's a plain word, which a file name
// or an option isn't.
func sentence(s string) string {
	if plainWord.MatchString(s) {
		return strings.ToUpper(s[:1]) + s[1:]
	}
	return s
}

// section returns the key of the section c goes in, or "other".
func (c commit) section() string {
	switch {
	case c.breaking != "":
		return "breaking"
	case c.typ == "docs" && c.scope == "contributing":
		// Contributor docs are for people working on the program, not using it.
		return "other"
	case c.typ == "feat", c.typ == "fix", c.typ == "perf", c.typ == "docs":
		return c.typ
	}
	return "other"
}

// commits returns the commits in the release, oldest first, without the
// reverted ones.
func commits(dir, tag, previous string) ([]commit, error) {
	rng := tag
	if previous != "" {
		rng = previous + ".." + tag
	}
	out, err := git(dir, "log", "--no-merges", "--reverse", "--format=%H%x1f%s%x1f%b%x1e", rng)
	if err != nil {
		return nil, err
	}
	var found []commit
	for record := range strings.SplitSeq(out, "\x1e") {
		fields := strings.Split(strings.TrimLeft(record, "\n"), "\x1f")
		if len(fields) == 3 {
			found = append(found, parseCommit(fields[0], fields[1], fields[2]))
		}
	}
	return withoutReverts(found), nil
}

// withoutReverts leaves out each commit that a later one reverts, and the
// revert. A revert of a commit from an earlier release stays.
func withoutReverts(log []commit) []commit {
	skip := map[string]bool{}
	for i, revert := range log {
		if revert.revertsSHA == "" && revert.revertsTitle == "" {
			continue
		}
		for _, c := range log[:i] {
			if skip[c.sha] {
				continue
			}
			var found bool
			if revert.revertsSHA != "" {
				found = strings.HasPrefix(c.sha, revert.revertsSHA)
			} else {
				found = c.subject == revert.revertsTitle
			}
			if found {
				skip[c.sha], skip[revert.sha] = true, true
				break
			}
		}
	}
	var kept []commit
	for _, c := range log {
		if !skip[c.sha] {
			kept = append(kept, c)
		}
	}
	return kept
}

// entry returns a list item with a link to the commit. Without text, it's the
// commit's subject.
func entry(c commit, url, text string) string {
	if text == "" {
		text = c.subject
	} else {
		text = sentence(text)
		if c.scope != "" {
			text = "**" + c.scope + ":** " + text
		}
	}
	return fmt.Sprintf("- %s ([`%s`](%s/commit/%s))\n", text, c.sha[:7], url, c.sha)
}

func notes(tag, repo, previous, message string, log []commit, sums string) string {
	url := "https://github.com/" + repo
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	if version, _, pre := strings.Cut(strings.TrimPrefix(tag, "v"), "-"); pre {
		p("> [!WARNING]\n> This is a pre-release, for trying things out before %s is final.\n\n", version)
	}
	if message != "" {
		p("%s\n\n", message)
	}

	dl := func(label, target string) string {
		return fmt.Sprintf("[%s](%s/releases/download/%s/%s_%s_%s)", label, url, tag, name, tag, target)
	}
	p("## Downloads\n\n")
	p("| System | Download |\n| --- | --- |\n")
	p("| Windows | %s · %s |\n", dl("x64", "windows-amd64.zip"), dl("arm64", "windows-arm64.zip"))
	p("| macOS (Intel and Apple silicon) | %s |\n", dl("universal", "darwin-universal.tar.xz"))
	p("| Linux | %s · %s |\n", dl("x64", "linux-amd64.tar.xz"), dl("arm64", "linux-arm64.tar.xz"))
	p("| Android | %s · %s |\n\n", dl("x64", "android-amd64.tar.xz"), dl("arm64", "android-arm64.tar.xz"))
	p("You also need [mpv](https://mpv.io/installation/), or [mpv.net](https://github.com/mpvnet-player/mpv.net/releases/latest) on Windows, or [Termux](https://termux.dev) and the [mpv app](https://github.com/mpv-android/mpv-android) on Android.\n\n")

	p("## What's changed\n\n")
	listed, untyped := 0, false
	var other []commit
	for _, c := range log {
		if c.section() == "other" {
			other = append(other, c)
			untyped = untyped || c.typ == ""
		}
	}
	for _, s := range sections {
		var lines []string
		for _, c := range log {
			if c.section() != s.key {
				continue
			}
			text := c.description
			if s.key == "breaking" {
				text = c.breaking
			}
			lines = append(lines, entry(c, url, text))
		}
		if len(lines) > 0 {
			p("### %s\n\n%s\n", s.title, strings.Join(lines, ""))
			listed += len(lines)
		}
	}
	// A commit that doesn't follow Conventional Commits may still change the
	// program.
	if listed == 0 && !untyped {
		p("No user-facing changes.\n\n")
	}
	if len(other) > 0 {
		p("<details>\n<summary>🧰 Other changes (%d)</summary>\n\n", len(other))
		for _, c := range other {
			p("%s", entry(c, url, ""))
		}
		p("\n</details>\n\n")
	}

	if previous != "" {
		p("**Full changelog:** [%s...%s](%s/compare/%s...%s)\n", previous, tag, url, previous, tag)
	} else {
		p("**Full changelog:** [all commits](%s/commits/%s)\n", url, tag)
	}

	p("\n<details>\n<summary>🔒 SHA-256</summary>\n\n```\n%s\n```\n\n", strings.TrimRight(sums, "\n"))
	p("Check an archive with its `.sha256` file, in the folder you downloaded both to. Replace `FILE` with the archive's name:\n\n")
	p("| System | Command |\n| --- | --- |\n")
	p("| macOS | `shasum -a 256 -c FILE.sha256` |\n")
	p("| Linux, Android | `sha256sum -c FILE.sha256` |\n")
	p("| Windows (PowerShell) | `$hash, $file = (Get-Content FILE.sha256) -split '\\s+'; (Get-FileHash $file).Hash -eq $hash` |\n\n")
	p("On Windows, it prints `True` if the archive is intact.\n\n</details>\n")
	return b.String()
}
