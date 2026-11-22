package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const url = "https://github.com/me/repo"

// newRepo makes an empty repository and runs git with an author and none of
// the machine's config, which may sign commits and tags.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, v := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(v, "Test")
	}
	for _, v := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(v, "test@example.com")
	}
	run(t, dir, "init", "-q")
	return dir
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// addCommit commits a change with message. The message is passed in a file so
// that it can have several lines on any system, and the change is the message
// added to a file, so that git revert has something to undo.
func addCommit(t *testing.T, dir, message string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "message")
	if err := os.WriteFile(file, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := os.OpenFile(filepath.Join(dir, "changes"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = changes.WriteString(message + "\n")
	if cerr := changes.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "changes")
	run(t, dir, "commit", "-q", "--cleanup=verbatim", "-F", file)
}

func subjects(log []commit) []string {
	var s []string
	for _, c := range log {
		s = append(s, c.subject)
	}
	return s
}

func link(sha string) string {
	return "([`" + sha[:7] + "`](" + url + "/commit/" + sha + "))"
}

func TestRanges(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "Initial commit")
	run(t, dir, "tag", "v1.0.0")
	addCommit(t, dir, "feat: add A")
	run(t, dir, "tag", "v1.1.0-beta.1")
	addCommit(t, dir, "fix: mend B")
	run(t, dir, "tag", "v1.1.0-beta.2")
	addCommit(t, dir, "refactor: change C")
	run(t, dir, "tag", "v1.1.0")
	addCommit(t, dir, "feat: add D")
	run(t, dir, "tag", "v1.2.0-rc.1")

	for _, tc := range []struct {
		tag, previous string
		subjects      []string
	}{
		// The first release lists everything.
		{"v1.0.0", "", []string{"Initial commit"}},
		// A pre-release goes back to the tag before it, whatever its kind...
		{"v1.1.0-beta.1", "v1.0.0", []string{"feat: add A"}},
		{"v1.1.0-beta.2", "v1.1.0-beta.1", []string{"fix: mend B"}},
		{"v1.2.0-rc.1", "v1.1.0", []string{"feat: add D"}},
		// ...and a final release to the previous final release, oldest first.
		{"v1.1.0", "v1.0.0", []string{"feat: add A", "fix: mend B", "refactor: change C"}},
	} {
		previous := previousTag(dir, tc.tag)
		if previous != tc.previous {
			t.Errorf("previousTag(%s) = %q, want %q", tc.tag, previous, tc.previous)
		}
		log, err := commits(dir, tc.tag, previous)
		if err != nil {
			t.Fatal(err)
		}
		if got := subjects(log); !slices.Equal(got, tc.subjects) {
			t.Errorf("commits(%s) = %q, want %q", tc.tag, got, tc.subjects)
		}
	}
}

func TestSectionsAndReverts(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "Initial commit")
	run(t, dir, "tag", "v1.0.0")
	for _, message := range []string{
		"feat: add --all-subs",
		"fix(hls): keep the first primary stream",
		"perf: start faster",
		"docs: explain --keep-playlist",
		"docs(contributing): explain the tests",
		"ci: run the race detector",
		"feat!: drop --output",
		"Tidy up",
		"feat: add a broken thing",
	} {
		addCommit(t, dir, message)
	}
	run(t, dir, "revert", "--no-edit", "HEAD")
	// Without git's line naming the commit, the subject finds it.
	addCommit(t, dir, "feat: add another broken thing")
	addCommit(t, dir, "revert: feat: add another broken thing")
	run(t, dir, "tag", "v2.0.0")

	log, err := commits(dir, "v2.0.0", previousTag(dir, "v2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range log {
		got[c.subject] = c.section()
	}
	want := map[string]string{
		"feat: add --all-subs":                    "feat",
		"fix(hls): keep the first primary stream": "fix",
		"perf: start faster":                      "perf",
		"docs: explain --keep-playlist":           "docs",
		"docs(contributing): explain the tests":   "other",
		"ci: run the race detector":               "other",
		"feat!: drop --output":                    "breaking",
		"Tidy up":                                 "other",
	}
	if len(got) != len(want) {
		t.Errorf("sections = %v, want %v", got, want)
	}
	for subject, section := range want {
		if got[subject] != section {
			t.Errorf("%q is in %q, want %q", subject, got[subject], section)
		}
	}
}

func TestRevertOfAnEarlierReleaseStays(t *testing.T) {
	revert := parseCommit(strings.Repeat("1", 40), `Revert "feat: add a thing"`, "This reverts commit "+strings.Repeat("0", 40)+".\n")
	if got := withoutReverts([]commit{revert}); len(got) != 1 {
		t.Errorf("withoutReverts left out the revert of a commit it doesn't have")
	}
}

func TestBreakingChangeFooter(t *testing.T) {
	c := parseCommit(strings.Repeat("0", 40), "feat!: replace --output",
		"Why.\r\n\r\nBREAKING CHANGE: Use --keep-playlist\r\ninstead.\r\nCo-Authored-By: A <a@b.c>\r\n")
	if want := "Replace --output. Use --keep-playlist instead."; c.breaking != want {
		t.Errorf("breaking = %q, want %q", c.breaking, want)
	}
}

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{
		"add a thing":      "Add a thing",
		"don't ask":        "Don't ask",
		"mpv's window":     "Mpv's window",
		"--keep-playlist":  "--keep-playlist",
		"go.mod is tidied": "go.mod is tidied",
	} {
		if got := sentence(in); got != want {
			t.Errorf("sentence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagMessage(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "feat: add a thing\n\nA body that isn't the tag's.")
	run(t, dir, "tag", "v1.0.0")
	run(t, dir, "tag", "-a", "-m", "v1.1.0", "v1.1.0")
	run(t, dir, "tag", "-a", "-m", "apple-event-streamer 1.2.0", "-m", "Adds --foo.", "v1.2.0")
	signature := "-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----"
	run(t, dir, "tag", "-a", "-m", "v1.3.0", "-m", "Adds --bar.", "-m", signature, "v1.3.0")
	for tag, want := range map[string]string{
		"v1.0.0": "",
		"v1.1.0": "",
		"v1.2.0": "Adds --foo.",
		"v1.3.0": "Adds --bar.",
	} {
		got, err := tagMessage(dir, tag)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("tagMessage(%s) = %q, want %q", tag, got, want)
		}
	}
}

func TestInstallHeading(t *testing.T) {
	readme := "# App\n\n```\n## Install from a code block\n```\n\n## Usage\n\n## Quick start\n"
	if got := installHeading(readme); got != "Quick start" {
		t.Errorf("installHeading = %q, want Quick start", got)
	}
	if got := installHeading("# App\n\n## Usage\n"); got != "" {
		t.Errorf("installHeading without one = %q", got)
	}
	if got := anchor("Download a release (easiest)"); got != "download-a-release-easiest" {
		t.Errorf("anchor = %q", got)
	}
}

func TestDownloads(t *testing.T) {
	got, err := downloads([]string{
		"abc  apple-event-streamer_v1.1.0_linux-amd64.tar.xz\n",
		"def *apple-event-streamer_v1.1.0_windows-arm64.zip\n123  apple-event-streamer_v1.1.0_darwin-universal.tar.xz\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range got {
		names = append(names, d.name+" | "+d.label+" | "+d.system)
	}
	if want := []string{
		"apple-event-streamer_v1.1.0_windows-arm64.zip | Windows arm64 | Windows",
		"apple-event-streamer_v1.1.0_darwin-universal.tar.xz | macOS, Apple silicon and Intel | macOS",
		"apple-event-streamer_v1.1.0_linux-amd64.tar.xz | Linux x64 | Linux",
	}; !slices.Equal(names, want) {
		t.Errorf("downloads = %q, want %q", names, want)
	}
	if _, err := downloads([]string{"123  other.zip\n"}); err == nil || !strings.Contains(err.Error(), "other.zip matches none") {
		t.Errorf("an unknown file gives %v", err)
	}
}

func TestNotes(t *testing.T) {
	a, b, c, d := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40), strings.Repeat("d", 40)
	log := []commit{
		parseCommit(a, "feat(picker): add a search", ""),
		parseCommit(b, "fix!: keep the playlist", "BREAKING CHANGE: Pass --keep-playlist to keep it."),
		parseCommit(c, "docs: explain --keep-playlist", ""),
		parseCommit(d, "ci: run the tests", ""),
	}
	windows := "apple-event-streamer_v1.1.0-rc.1+abc1234_windows-amd64.zip"
	linux := "apple-event-streamer_v1.1.0-rc.1+abc1234_linux-amd64.tar.xz"
	archives, err := downloads([]string{"abc123  " + linux + "\n", "def456  " + windows + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	got := notes("v1.1.0-rc.1", "me/repo", "v1.0.0", log, archives, "Adds a search.", "# App\n\n## Quick start\n")
	want := strings.Join([]string{
		"> [!WARNING]",
		"> This is a pre-release, for trying things out before 1.1.0 is final. Please report problems in [Issues](" + url + "/issues).",
		"",
		"Adds a search.",
		"",
		"## Download",
		"",
		"| File | For |",
		"| --- | --- |",
		"| [" + windows + "](" + url + "/releases/download/v1.1.0-rc.1/" + windows + ") | Windows x64 |",
		"| [" + linux + "](" + url + "/releases/download/v1.1.0-rc.1/" + linux + ") | Linux x64 |",
		"",
		"For how to install it, see [Quick start](" + url + "/blob/v1.1.0-rc.1/README.md#quick-start) in the README.",
		"",
		"## What's changed",
		"",
		"### ⚠️ Breaking changes",
		"",
		"- Keep the playlist. Pass --keep-playlist to keep it. " + link(b),
		"",
		"### ✨ Features",
		"",
		"- **picker:** Add a search " + link(a),
		"",
		"### 📖 Documentation",
		"",
		"- Explain --keep-playlist " + link(c),
		"",
		"<details>",
		"<summary>🧰 Other changes (1)</summary>",
		"",
		"- ci: run the tests " + link(d),
		"",
		"</details>",
		"",
		"**Full changelog:** [v1.0.0...v1.1.0-rc.1](" + url + "/compare/v1.0.0...v1.1.0-rc.1)",
		"",
		"<details>",
		"<summary>🔒 SHA-256</summary>",
		"",
		"```",
		"def456  " + windows,
		"abc123  " + linux,
		"```",
		"",
		"To check your downloads, put them and their `.sha256` files in a folder of their own, and run the command for your system there. Delete older copies first, so your browser doesn't rename the new ones.",
		"",
		"- Linux: `sha256sum -c *.sha256`",
		"- Windows, in PowerShell: `Get-ChildItem *.sha256 | ForEach-Object { $hash, $file = (Get-Content $_.FullName) -split '\\s+'; \"${file}: $((Get-FileHash $file).Hash -eq $hash)\" }`",
		"",
		"</details>",
		"",
	}, "\n")
	if got != want {
		t.Errorf("notes =\n%s\nwant\n%s", got, want)
	}
}

func TestNotesWithoutUserFacingChanges(t *testing.T) {
	ci := parseCommit(strings.Repeat("0", 40), "ci: run the tests", "")
	text := notes("v1.0.0", "me/repo", "", []commit{ci}, nil, "", "")
	for _, want := range []string{
		"## Download\n\nFor how to install it, see the [README](" + url + "/blob/v1.0.0/README.md).",
		"## What's changed\n\nNo user-facing changes.\n",
		"**Full changelog:** [all commits](" + url + "/commits/v1.0.0)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the notes don't have %q:\n%s", want, text)
		}
	}
	if strings.HasPrefix(text, ">") || strings.Contains(text, "SHA-256") {
		t.Errorf("the notes have a warning or checksums:\n%s", text)
	}
	// A commit without a type may still change the program.
	untyped := parseCommit(strings.Repeat("1", 40), "Tidy up", "")
	if text := notes("v1.0.0", "me/repo", "", []commit{ci, untyped}, nil, "", ""); strings.Contains(text, "No user-facing") {
		t.Errorf("the notes say there are no user-facing changes with an untyped commit:\n%s", text)
	}
}

func TestTagPattern(t *testing.T) {
	for _, tag := range []string{"v1.0.0", "v1.0.0-alpha.1", "v1.0.0-beta.2", "v1.0.0-rc.10"} {
		if !tagPattern.MatchString(tag) {
			t.Errorf("%s is refused", tag)
		}
	}
	for _, tag := range []string{"1.0.0", "v1.0", "v1.0.0-", "v1.0.0-test.1", "v1.0.0-beta", "v1.0.0-beta.0", "release-1"} {
		if tagPattern.MatchString(tag) {
			t.Errorf("%s is accepted", tag)
		}
	}
}
