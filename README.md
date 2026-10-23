# apple-event-streamer

Watch the current [Apple Event](https://www.apple.com/apple-events/) — live or
replay — in [mpv](https://mpv.io) or [mpv.net](https://github.com/mpvnet-player/mpv.net),
at exactly the quality you choose.

It finds the event's master playlist, lets you pick one rendition
(video › audio › subtitles › resolution › bitrate), writes a single-rendition
`.m3u8` to the system temp folder and hands the terminal over to the player.
The command lasts until you quit the player, then the playlist is deleted.

Single self-contained executable for Windows and Linux (x64 and arm64) and
macOS (one universal2 binary for Intel and Apple silicon, macOS 12 Monterey or
later). No runtime dependencies other than the player.

## Usage

```
apple-event-streamer                 # interactive picker
apple-event-streamer --best          # highest-bitrate SDR + AAC rendition
apple-event-streamer --url URL       # use a supplied master playlist (URL or file)
apple-event-streamer --no-launch     # create and print playlist path only
apple-event-streamer --keep-playlist # preserve the temporary playlist after mpv exits
apple-event-streamer --mpv PATH      # explicit mpv executable
apple-event-streamer --mpvnet PATH   # explicit mpvnet executable
apple-event-streamer --timeout SEC   # network timeout (default 20)
apple-event-streamer --verbose
apple-event-streamer --version
apple-event-streamer --help
```

Selection options combine freely, e.g. `--best --subs --sub-lang en --ad`:

| Option | Meaning |
| --- | --- |
| `--best` | Pick the highest-bitrate stream without asking (SDR + AAC unless `--range`/`--audio` say otherwise). |
| `--range sdr\|hevc\|hdr10\|dv` | Video range: SDR (H.264), SDR [HEVC], HDR10, Dolby Vision. Skips that menu. |
| `--audio aac\|atmos` | Audio format. Skips that menu. |
| `--max-res RES` | Highest resolution to offer, e.g. `1080`, `1440p`, `4k`. |
| `--subs` | Include subtitles (all languages unless `--sub-lang`). Off by default. |
| `--sub-lang LANGS` | Comma-separated languages, e.g. `en` or `en,fr`; implies `--subs`. `en` also matches `en-GB`, `zh` matches every Chinese track. |
| `--ad` | Use audio-description tracks *instead of* regular audio. Off by default. |
| `--asl` | Play the American Sign Language version of the event. |
| `--list` | Print every available stream and exit. |
| `-- ARGS` | Pass everything after `--` to the player, e.g. `-- --fs --volume=70`. |

### Interactive picker

`↑`/`↓` (or `j`/`k`) to move, `Enter`/`→` to choose, `←`/`Esc`/`Backspace` to
go back, `1`–`9` to choose directly, `q` to quit. The mouse works too: click to
choose, scroll to move, right-click (or the *Back* button) to go back.

All four video ranges are always listed; any the playlist doesn't carry
(the live stream has no HDR10, for example) is greyed out and skipped by the
cursor. The subtitles step comes after audio and defaults to *None*; it is
skipped when `--subs`/`--sub-lang` already answer it.

## Player

* **Windows:** `mpvnet` on `PATH` is preferred, falling back to `mpv`.
  mpv.net is started with `--process-instance=multi` so it can't hand the file
  to an already-running window and exit early.
* **macOS / Linux:** `mpv` on `PATH` (plus common install locations such as
  `/Applications/mpv.app`).

The player always gets
`--demuxer-lavf-o=protocol_whitelist="file,crypto,data,https,http,tls,tcp"`,
which lets a local playlist reference `https` streams. When subtitles are
included, `strict=experimental` is added to the same option because FFmpeg's
HLS demuxer otherwise ignores subtitle renditions.

On every platform the player runs in the same terminal and the app waits for
it, then deletes the playlist (unless `--keep-playlist`) and exits with the
player's exit code, so prompt timers measure from launch to player exit.

## Playlists

The output is written to `<temp>/apple-event-<short-uuid>.m3u8` (a 22-character
base57 UUID) and contains one `EXT-X-STREAM-INF`, its audio rendition and, only
if requested, its subtitles. I-frame playlists are dropped. For live events,
Apple lists each variant on primary and backup endpoints; the first primary
endpoint is used and backups (`-b` groups/hosts) are discarded.

The stream is discovered through the `url-json` meta tag on
apple.com/apple-events, which points at a JSON document whose `videoSrc.hls` is
the master playlist for both live and replay.

## Installing

Download the archive for your platform from
[Releases](https://github.com/Mahir007/apple-event-streamer/releases), extract
it and put `apple-event-streamer` somewhere on your `PATH`. Each archive holds
the executable, `LICENSE` and `THIRD_PARTY_LICENSES` (the notices of the
open-source libraries and Go standard library built into it), and has a matching `<archive>.sha256` checksum
(verify with `sha256sum -c <archive>.sha256`, or `shasum -a 256 -c` on macOS).

| Platform | Archive | Requires |
| --- | --- | --- |
| Windows x64 / arm64 | `…_windows-amd64.zip`, `…_windows-arm64.zip` | Windows 10+ |
| macOS (Intel + Apple silicon) | `…_darwin-universal.tar.xz` | macOS 12 Monterey+ |
| Linux x64 / arm64 | `…_linux-amd64.tar.xz`, `…_linux-arm64.tar.xz` | any (static binary) |

On macOS the binary is ad-hoc signed but not notarized, so a copy downloaded
with a browser is quarantined by Gatekeeper. Clear that with
`xattr -d com.apple.quarantine apple-event-streamer` (or download with `curl`).

## Building

Requires Go 1.26+. The Go version sets the oldest supported macOS: releases
are built with the Go version pinned in `.go-version` (1.26.10), which targets
macOS 12 Monterey; building with Go 1.27 or later raises that to macOS 13
Ventura. To reproduce a release build with a newer local Go, let it fetch the
pinned toolchain:

```sh
GOTOOLCHAIN=go$(cat .go-version) make package
```

```sh
make build     # current platform
make dist      # all targets into dist/ (macOS as a universal2 binary)
make package   # dist + archives and .sha256 files (needs xz, zip, advzip)
make test
```

Binaries are built with `CGO_ENABLED=0 -trimpath -ldflags "-s -w"`: static,
with the symbol table and DWARF debug info stripped. Archives use maximum
compression: `xz -9e` for `.tar.xz`, and for Windows a standard `.zip`
recompressed with zopfli (`advzip -z -4`, about 4% smaller than `zip -9`).

Executables are deliberately not UPX-packed. UPX can't pack Windows arm64 or
macOS binaries (and would break the arm64 code signature), the packed files
download no smaller than the xz archives, it decompresses the whole program in
memory on every launch, and packed Windows executables are often flagged by
antivirus software.

The universal2 binary is assembled by `tools/macuniversal`, a small Go
equivalent of `lipo -create`, so it can be built on any OS.
`tools/thirdparty` generates `THIRD_PARTY_LICENSES` from the modules
`go list -deps` reports for every target, so it always matches what is linked.

## CI and releases

Every push and pull request runs `.github/workflows/ci.yml`, which tests and
packages every target and attaches the archives to the workflow run as an
artifact. It never publishes anything.

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`,
which uses the pinned Go toolchain to test, build and package every
target and publishes a GitHub release
with the archives and their `.sha256` files. Tags containing a `-` (e.g. `v1.1.0-rc.1`)
are marked as pre-releases.

```sh
git tag v1.0.0
git push origin v1.0.0
```
