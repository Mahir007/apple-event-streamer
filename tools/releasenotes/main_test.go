package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

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

func TestRanges(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "Initial commit")
	run(t, dir, "tag", "v1.0.0")
	addCommit(t, dir, "feat: add A")
	run(t, dir, "tag", "v1.1.0-rc.1")
	addCommit(t, dir, "fix: mend B")
	run(t, dir, "tag", "v1.1.0-rc.2")
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
		{"v1.1.0-rc.1", "v1.0.0", []string{"feat: add A"}},
		{"v1.1.0-rc.2", "v1.1.0-rc.1", []string{"fix: mend B"}},
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
	run(t, dir, "tag", "v1.0.0")

	log, err := commits(dir, "v1.0.0", "")
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

func TestRevertOfAnEarlierRelease(t *testing.T) {
	revert := parseCommit(strings.Repeat("1", 40), `Revert "feat: add a thing"`, "This reverts commit "+strings.Repeat("0", 40)+".\n")
	if got := withoutReverts([]commit{revert}); len(got) != 1 {
		t.Errorf("withoutReverts left out the revert of a commit it doesn't have")
	}
}

func TestBreakingFooter(t *testing.T) {
	c := parseCommit(strings.Repeat("0", 40), "feat!: replace --output",
		"Why.\r\n\r\nBREAKING CHANGE: Use --keep-playlist\r\ninstead.\r\nCo-Authored-By: A <a@b.c>\r\n")
	if want := "Replace --output. Use --keep-playlist instead."; c.breaking != want {
		t.Errorf("breaking = %q, want %q", c.breaking, want)
	}
}

func TestSentence(t *testing.T) {
	for in, want := range map[string]string{
		"add a thing":      "Add a thing",
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
	run(t, dir, "tag", "-a", "-m", "v1.2.0", "-m", "Adds --foo.", "v1.2.0")
	for tag, want := range map[string]string{
		"v1.0.0": "",
		"v1.1.0": "",
		"v1.2.0": "Adds --foo.",
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

// link matches the link after each commit in the notes.
const link = "\\(\\[`[0-9a-f]{7}`\\]\\(https://github\\.com/me/repo/commit/[0-9a-f]{40}\\)\\)"

func TestNotes(t *testing.T) {
	dir := newRepo(t)
	addCommit(t, dir, "Initial commit")
	run(t, dir, "tag", "v1.0.0")
	addCommit(t, dir, "feat(picker): add a search")
	addCommit(t, dir, "fix!: keep the playlist\n\nBREAKING CHANGE: Pass --keep-playlist to keep it.")
	addCommit(t, dir, "ci: run the tests")
	run(t, dir, "tag", "v1.1.0-rc.1")
	log, err := commits(dir, "v1.1.0-rc.1", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	sums := "abc123  apple-event-streamer_v1.1.0-rc.1_linux-amd64.tar.xz\n"
	text := notes("v1.1.0-rc.1", "me/repo", "v1.0.0", "Adds a search.", log, sums)

	for _, want := range []string{
		"^> \\[!WARNING\\]\n> This is a pre-release, for trying things out before 1\\.1\\.0 is final\\.\n\nAdds a search\\.\n\n## Downloads\n",
		"\\| Linux \\| \\[x64\\]\\(https://github\\.com/me/repo/releases/download/v1\\.1\\.0-rc\\.1/apple-event-streamer_v1\\.1\\.0-rc\\.1_linux-amd64\\.tar\\.xz\\)",
		"### ⚠️ Breaking changes\n\n- Keep the playlist\\. Pass --keep-playlist to keep it\\. " + link + "\n\n### ✨ Features\n\n- \\*\\*picker:\\*\\* Add a search " + link + "\n\n<details>",
		"<summary>🧰 Other changes \\(1\\)</summary>\n\n- ci: run the tests " + link + "\n",
		"\\*\\*Full changelog:\\*\\* \\[v1\\.0\\.0\\.\\.\\.v1\\.1\\.0-rc\\.1\\]\\(https://github\\.com/me/repo/compare/v1\\.0\\.0\\.\\.\\.v1\\.1\\.0-rc\\.1\\)",
		"```\nabc123  apple-event-streamer_v1\\.1\\.0-rc\\.1_linux-amd64\\.tar\\.xz\n```",
	} {
		if !regexp.MustCompile(want).MatchString(text) {
			t.Errorf("the notes don't match %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"### 🐛 Fixes", "No user-facing changes"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the notes have %q:\n%s", unwanted, text)
		}
	}
}

func TestNotesWithoutUserFacingChanges(t *testing.T) {
	ci := parseCommit(strings.Repeat("0", 40), "ci: run the tests", "")
	text := notes("v1.0.0", "me/repo", "", "", []commit{ci}, "")
	if !strings.Contains(text, "## What's changed\n\nNo user-facing changes.\n") {
		t.Errorf("the notes don't say there are no user-facing changes:\n%s", text)
	}
	if strings.Contains(text, "WARNING") {
		t.Errorf("the notes for a final release have a pre-release warning:\n%s", text)
	}
	if !strings.Contains(text, "**Full changelog:** [all commits](https://github.com/me/repo/commits/v1.0.0)") {
		t.Errorf("the notes for the first release don't link all commits:\n%s", text)
	}
	// A commit without a type may still change the program.
	untyped := parseCommit(strings.Repeat("1", 40), "Tidy up", "")
	if text := notes("v1.0.0", "me/repo", "", "", []commit{ci, untyped}, ""); strings.Contains(text, "No user-facing") {
		t.Errorf("the notes say there are no user-facing changes with an untyped commit:\n%s", text)
	}
}
