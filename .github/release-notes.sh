#!/usr/bin/env bash
# Prints the release notes for a tag as Markdown: the tag message (if it says
# more than the tag name), a download table, and the commit subjects since the
# previous release. A stable tag is compared with the previous stable tag, so
# its notes cover all of its release candidates; a pre-release is compared
# with the tag before it. Commits that only touch .github/ or .gitignore are
# left out.
#
# Usage: .github/release-notes.sh TAG [OWNER/REPO]
set -euo pipefail

tag=${1:?usage: release-notes.sh TAG [OWNER/REPO]}
repo=${2:-${GITHUB_REPOSITORY:-Mahir007/apple-event-streamer}}
name=apple-event-streamer
url=https://github.com/$repo

exclude=()
[[ $tag == *-* ]] || exclude=(--exclude '*-*')
prev=$(git describe --tags --abbrev=0 "${exclude[@]}" "$tag^" 2>/dev/null || true)

# Annotated tag message without its signature, and without its first line
# when that is just the tag name.
msg=$(git tag -l --format='%(contents)' "$tag" | sed '/^-----BEGIN /,$d' |
	sed "1{/^$tag\$/d}")
[[ -n ${msg//[[:space:]]/} ]] && printf '%s\n\n' "$(printf '%s' "$msg" | sed '/./,$!d')"

dl() { printf '[%s](%s/releases/download/%s/%s)' "$1" "$url" "$tag" "${name}_${tag}_$2"; }

cat <<EOF
## Downloads

| System | Download |
| --- | --- |
| Windows | $(dl x64 windows-amd64.zip) · $(dl arm64 windows-arm64.zip) |
| macOS (Intel and Apple silicon) | $(dl universal darwin-universal.tar.xz) |
| Linux | $(dl x64 linux-amd64.tar.xz) · $(dl arm64 linux-arm64.tar.xz) |

You also need [mpv](https://mpv.io/installation/), or [mpv.net](https://github.com/mpvnet-player/mpv.net/releases/latest) on Windows.

## Changes

EOF

range=$tag
[[ -n $prev ]] && range=$prev..$tag
git log --reverse --no-merges --format="- %s ([%h]($url/commit/%H))" "$range" \
	-- . ':(exclude).github' ':(exclude).gitignore'

if [[ -n $prev ]]; then
	printf '\nFull changelog: [%s...%s](%s/compare/%s...%s)\n' "$prev" "$tag" "$url" "$prev" "$tag"
fi
