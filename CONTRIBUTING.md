# Contributing

## Building

You need Go 1.26 or later. Releases use the Go version pinned in
`.go-version` (1.26.10). The Go version decides the oldest macOS the binary
runs on: Go 1.26 supports macOS 12 Monterey, Go 1.27 and later need macOS 13
Ventura.

```sh
make build     # current platform
make dist      # all targets into dist/ (macOS as a universal2 binary; needs the NDK)
make package   # dist plus archives and .sha256 files (needs xz, zip, advzip)
make test
make race      # tests under the race detector (needs cgo)
make lint      # staticcheck and govulncheck for Linux, Android, Windows and macOS
```

To build a release with a newer local Go, let Go fetch the pinned version:

```sh
GOTOOLCHAIN=go$(cat .go-version) make package
```

Binaries are built with `CGO_ENABLED=0 -trimpath -tags nethttpomithttp2
-ldflags "-s -w"`. The tag leaves out HTTP/2, which saves about 400 KB: the
app only fetches three small files, and mpv does the streaming.

Android is the exception, and needs cgo: Go's own DNS resolver doesn't work on
Android, and `android/amd64` can't link without cgo. The static Linux binaries
aren't an alternative. They can't look up hosts either, crash on Android 11 in
`exec.LookPath`, and won't start at all in Termux from Google Play.

Android builds use the NDK version in `.ndk-version`, from `$ANDROID_NDK` or
`$ANDROID_HOME/ndk/`, and target API 23 (Android 6), the oldest version the mpv
app runs on. The binaries are aligned for 16 KB pages. `make vet` and `make
lint` only check `android/arm64`, because `android/amd64` won't type-check
without cgo.

## Archives

`.tar.xz` archives use `xz -9e`. Windows `.zip` files are recompressed with
zopfli (`advzip -z -4`), which makes them about 4% smaller than `zip -9`.

Not UPX-packed: it can't pack Windows arm64 or macOS, isn't smaller than xz,
and trips antivirus.

## Tools

- `tools/androidelf` clears `DF_1_PIE` in the Android binaries. Android 6's
  linker doesn't know the flag and prints a warning for it on every run.
- `tools/macuniversal` joins the two macOS binaries into one universal2
  binary. It's a Go `lipo -create`, so it builds anywhere.
- `tools/thirdparty` writes `THIRD_PARTY_LICENSES` from the modules that
  `go list -deps` reports for each target, plus the NDK's C startup code.
- `tools/winres` writes the Windows version resource (the description shown in
  Task Manager, the version and the copyright) as a `.syso` file that the
  linker embeds.

## How the app works

1. It reads the `url-json` meta tag on apple.com/apple-events. The tag points
   at a JSON document whose `videoSrc.hls` is the master playlist, for both
   live events and replays.
2. It writes a playlist with only the chosen stream: one `EXT-X-STREAM-INF`,
   its audio and any subtitles asked for. I-frame playlists are dropped. Live
   events list each stream on primary and backup servers; only the first
   primary is kept.
3. It runs the player in the same terminal and waits for it. Then it deletes
   the playlist (unless `--keep-playlist` is given) and exits with the
   player's exit code.

The player always gets
`--demuxer-lavf-o=protocol_whitelist="file,crypto,data,https,http,tls,tcp"`,
so a local playlist can load `https` streams. With subtitles,
`strict=experimental` is added to the same option, because FFmpeg's HLS
demuxer ignores subtitle tracks without it. mpv.net is started with
`--process-instance=multi`, so it can't pass the file to a window that's
already open and exit early.

On Android the default player is the mpv app, which works differently:

- It runs apart from the terminal, and Android has no way to wait for it to
  close or read its exit code. So the app serves the playlist on `127.0.0.1`,
  starts mpv with an intent through Termux's `am`, and exits five seconds after
  mpv last fetched the playlist.
- It gets the playlist over HTTP because Termux's temporary folder is private
  and shared storage needs a permission in both apps.
- An intent can't carry mpv options, so the two above can't be passed. The
  app's FFmpeg needs neither when the playlist comes over HTTP.
- `am`, and any player given with `--mpv`, run through `/system/bin/sh`. Termux
  from Google Play can only run its programs with `termux-exec`, an `LD_PRELOAD`
  library the shell loads and Go's `execve` bypasses. `/system/bin/am` itself
  is refused from an app.
- The picker asks for mouse mode 1002 instead of 1003, which Termux ignores.
  Its help says tap and swipe, as Termux sends a swipe as the wheel and keeps
  right-click for its own menu.

## CI

`.github/workflows/ci.yml` runs on every push to a branch:

- lint: `go vet` for every target, staticcheck, govulncheck and actionlint.
- test: the tests on Ubuntu, macOS, Windows and Android, and on Ubuntu also
  under the race detector. Android's run as x86_64 in `termux-docker`, with
  `make test-android`.
- build: every target's archive, with the pinned Go version. It runs the Linux
  binary, and keeps the archives for a day as the run's `release` artifact.
  On a branch the version is `v0.0.0-dev+<commit>`.
- check: extracts the macOS, Windows and Android archives and runs the binary.
  On macOS it runs both halves of the universal2 binary, x86_64 through Rosetta,
  and fails if the runner has no Rosetta. Android's run in `termux-docker`,
  which has Android's libc and linker with Termux's tools. It has neither
  Android's seccomp filter nor its apps, so playback needs a real device or an
  emulator.

The jobs use `.github/actions/setup`. It restores the newest Go module and
build cache for the job, falling back to older commits and `go.sum` versions,
so repeat runs only rebuild what changed. Only successful pushes to `main` save
the cache. Archives are compressed in parallel (they can't be cached).

## Releases

Pushing a tag that starts with `v` runs `.github/workflows/release.yml`. It
runs CI with the tag's version, then publishes a GitHub release with CI's
archives, their `.sha256` files and an attestation of where they were built.
Tags with a `-` (such as `v1.1.0-rc.1`) are marked as pre-releases.

```sh
git tag v1.0.0
git push origin v1.0.0
```

`.github/release-notes.sh` writes the release notes: a download table and the
commit subjects since the previous release. A stable release lists everything
since the previous stable release, including its release candidates. Commits
that only change `.github/` or `.gitignore` are left out. If the tag message
says more than the tag name, it goes at the top:

```sh
git tag -s v1.1.0 -m "v1.1.0" -m "Adds --foo. Linux builds now need glibc 2.31."
```

Run the script with a tag to preview the notes.
