// Command releasenotes prints the notes for a GitHub release as Markdown: the
// tag's summary, a download table, the commits since the previous release
// grouped by their Conventional Commits type, and the checksums. Run it in the
// repository, with the release's .sha256 files, which name its archives.
//
// A pre-release lists the commits since the tag before it, of either kind. A
// final release lists everything since the previous final release, so its
// notes include its release candidates'.
//
//	go run ./tools/releasenotes TAG OWNER/REPO SHA256FILE...
package main

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	tagPattern     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[1-9][0-9]*)?$`)
	subjectPattern = regexp.MustCompile(`^([a-zA-Z]+)(?:\(([^)]+)\))?(!)?: (.+)$`)
	breakingLine   = regexp.MustCompile(`^BREAKING[ -]CHANGE: (.*)$`)
	footerLine     = regexp.MustCompile(`^[A-Za-z-]+(?:: | #)`)
	plainWord      = regexp.MustCompile(`^[a-z]+('[a-z]+)?(\s|$)`)
	// git revert names the reverted commit in the body and quotes its subject
	// in the revert's subject, which may be reworded to revert: <subject>.
	revertsLine   = regexp.MustCompile(`This reverts commit ([0-9a-f]{7,40})`)
	revertSubject = regexp.MustCompile(`^Revert "(.+)"$`)
	heading       = regexp.MustCompile(`^#{2,3} +(.+?)[ #]*$`)
	aboutInstall  = regexp.MustCompile(`(?i)quick ?start|install|download`)
	notInAnchor   = regexp.MustCompile(`[^\p{L}\p{N}_\- ]`)
)

// The files a release has: a glob for the name, what the file is for, and its
// system, in the order of the download table.
var kinds = []struct{ glob, label, system string }{
	{"*_windows-amd64.zip", "Windows x64", "Windows"},
	{"*_windows-arm64.zip", "Windows arm64", "Windows"},
	{"*_darwin-universal.tar.xz", "macOS, Apple silicon and Intel", "macOS"},
	{"*_linux-amd64.tar.xz", "Linux x64", "Linux"},
	{"*_linux-arm64.tar.xz", "Linux arm64", "Linux"},
	{"*_android-amd64.tar.xz", "Android x64", "Android"},
	{"*_android-arm64.tar.xz", "Android arm64", "Android"},
}

// The command that checks the downloads on each system, in the order they're
// listed.
var checks = []struct{ system, command string }{
	{"macOS", "macOS: `shasum -a 256 -c *.sha256`"},
	{"Linux", "Linux: `sha256sum -c *.sha256`"},
	{"Android", "Android: `sha256sum -c *.sha256`"},
	{"Windows", "Windows, in PowerShell: `Get-ChildItem *.sha256 | ForEach-Object { $hash, $file = (Get-Content $_.FullName) -split '\\s+'; \"${file}: $((Get-FileHash $file).Hash -eq $hash)\" }`"},
}

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
	var sums []string
	for _, p := range os.Args[3:] {
		data, err := os.ReadFile(p)
		if err != nil {
			fail(err)
		}
		sums = append(sums, string(data))
	}
	archives, err := downloads(sums)
	if err != nil {
		fail(err)
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
	readme, _ := git(".", "show", tag+":README.md")
	fmt.Print(notes(tag, repo, previous, log, archives, message, readme))
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

// tagMessage returns an annotated tag's message without its first line and
// its signature. A lightweight tag has none: for-each-ref would give the
// tagged commit's message instead.
func tagMessage(dir, tag string) (string, error) {
	out, err := git(dir, "for-each-ref", "--format=%(objecttype)%00%(contents)", "refs/tags/"+tag)
	if err != nil {
		return "", err
	}
	kind, contents, _ := strings.Cut(out, "\x00")
	if kind != "tag" {
		return "", nil
	}
	lines := strings.Split(contents, "\n")[1:]
	for i, line := range lines {
		if strings.HasPrefix(line, "-----BEGIN ") {
			lines = lines[:i]
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

// installHeading returns the README's first ## or ### heading about a quick
// start, installing or downloading, or "".
func installHeading(readme string) string {
	fenced := false
	for line := range strings.Lines(readme) {
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if m := heading.FindStringSubmatch(line); !fenced && m != nil && aboutInstall.MatchString(m[1]) {
			return m[1]
		}
	}
	return ""
}

// anchor returns the id GitHub gives a heading.
func anchor(h string) string {
	return strings.ReplaceAll(notInAnchor.ReplaceAllString(strings.ToLower(h), ""), " ", "-")
}

// A file in the release, from its line in a .sha256 file.
type download struct {
	name, label, system, checksum string
	order                         int
}

// downloads returns the archives the .sha256 files name, in the order of
// kinds.
func downloads(sums []string) ([]download, error) {
	var found []download
	for _, text := range sums {
		for line := range strings.Lines(text) {
			line = strings.TrimRight(line, "\r\n")
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			name := strings.TrimPrefix(strings.TrimSpace(line[len(fields[0]):]), "*")
			i := slices.IndexFunc(kinds, func(f struct{ glob, label, system string }) bool {
				ok, _ := path.Match(f.glob, name)
				return ok
			})
			if i < 0 {
				return nil, fmt.Errorf("%s matches none of the files in kinds", name)
			}
			found = append(found, download{name, kinds[i].label, kinds[i].system, line, i})
		}
	}
	slices.SortStableFunc(found, func(a, b download) int {
		return cmp.Or(cmp.Compare(a.order, b.order), cmp.Compare(a.name, b.name))
	})
	return found, nil
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
	return fmt.Sprintf("- %s ([`%s`](%s/commit/%s))", text, c.sha[:7], url, c.sha)
}

func notes(tag, repo, previous string, log []commit, archives []download, message, readme string) string {
	url := "https://github.com/" + repo
	var parts []string

	if version, _, pre := strings.Cut(strings.TrimPrefix(tag, "v"), "-"); pre {
		parts = append(parts, "> [!WARNING]\n> This is a pre-release, for trying things out before "+
			version+" is final. Please report problems in [Issues]("+url+"/issues).")
	}
	if message != "" {
		parts = append(parts, message)
	}

	download := []string{"## Download"}
	if len(archives) > 0 {
		rows := []string{"| File | For |", "| --- | --- |"}
		for _, f := range archives {
			rows = append(rows, fmt.Sprintf("| [%s](%s/releases/download/%s/%s) | %s |", f.name, url, tag, f.name, f.label))
		}
		download = append(download, strings.Join(rows, "\n"))
	}
	link := url + "/blob/" + tag + "/README.md"
	if h := installHeading(readme); h != "" {
		download = append(download, fmt.Sprintf("For how to install it, see [%s](%s#%s) in the README.", h, link, anchor(h)))
	} else {
		download = append(download, fmt.Sprintf("For how to install it, see the [README](%s).", link))
	}
	parts = append(parts, strings.Join(download, "\n\n"))

	changes := []string{"## What's changed"}
	listed, untyped := 0, false
	var other []string
	for _, c := range log {
		if c.section() == "other" {
			other = append(other, entry(c, url, ""))
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
			changes = append(changes, "### "+s.title+"\n\n"+strings.Join(lines, "\n"))
			listed += len(lines)
		}
	}
	// A commit that doesn't follow Conventional Commits may still change the
	// program.
	if listed == 0 && !untyped {
		changes = append(changes, "No user-facing changes.")
	}
	if len(other) > 0 {
		changes = append(changes, fmt.Sprintf("<details>\n<summary>🧰 Other changes (%d)</summary>\n\n%s\n\n</details>",
			len(other), strings.Join(other, "\n")))
	}
	if previous != "" {
		changes = append(changes, fmt.Sprintf("**Full changelog:** [%s...%s](%s/compare/%s...%s)", previous, tag, url, previous, tag))
	} else {
		changes = append(changes, fmt.Sprintf("**Full changelog:** [all commits](%s/commits/%s)", url, tag))
	}
	parts = append(parts, strings.Join(changes, "\n\n"))

	if len(archives) > 0 {
		var sums, commands []string
		systems := map[string]bool{}
		for _, f := range archives {
			sums = append(sums, f.checksum)
			systems[f.system] = true
		}
		for _, c := range checks {
			if systems[c.system] {
				commands = append(commands, "- "+c.command)
			}
		}
		parts = append(parts, "<details>\n<summary>🔒 SHA-256</summary>\n\n```\n"+strings.Join(sums, "\n")+"\n```\n\n"+
			"To check your downloads, put them and their `.sha256` files in a folder of their own, and run the command "+
			"for your system there. Delete older copies first, so your browser doesn't rename the new ones.\n\n"+
			strings.Join(commands, "\n")+"\n\n</details>")
	}
	return strings.Join(parts, "\n\n") + "\n"
}
