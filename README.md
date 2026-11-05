# apple-event-streamer

Plays the current [Apple Event](https://www.apple.com/apple-events/) (live or
replay) in [mpv](https://mpv.io),
[mpv.net](https://github.com/mpvnet-player/mpv.net) or the
[mpv app](https://github.com/mpv-android/mpv-android) on Android.

```
 Apple Event Streamer   VOD

 Choose a stream

 Video  1/4

 ❯ SDR            H.264 / AVC
   SDR [HEVC]     H.265 / HEVC
   HDR10          HEVC · PQ
   Dolby Vision   HEVC · Profile 5

 ↑/↓ move · enter select · ← back · q quit · mouse: click, wheel, right-click back
  ‹ Back    Quit
```

## Quick start

You need mpv, or mpv.net on Windows, or [Termux](https://termux.dev) and the
mpv app on Android. Download the app from
[Releases](https://github.com/Mahir007/apple-event-streamer/releases), then:

```sh
apple-event-streamer          # pick a stream from a menu
apple-event-streamer --best   # highest-bitrate SDR + AAC stream
apple-event-streamer --list   # list every stream
```

## Installing

Download the archive for your system, extract it and put
`apple-event-streamer` somewhere on your `PATH`.

| System | Archive | Needs |
| --- | --- | --- |
| Windows x64 / arm64 | `…_windows-amd64.zip`, `…_windows-arm64.zip` | Windows 10 or later |
| macOS (Intel and Apple silicon) | `…_darwin-universal.tar.xz` | macOS 12 Monterey or later |
| Linux x64 / arm64 | `…_linux-amd64.tar.xz`, `…_linux-arm64.tar.xz` | any distribution |
| Android x64 / arm64 | `…_android-amd64.tar.xz`, `…_android-arm64.tar.xz` | Android 6 or later, Termux, the mpv app |

Each archive comes with a `.sha256` file. Check it with
`sha256sum -c <archive>.sha256`, or `shasum -a 256 -c <archive>.sha256` on
macOS. On macOS, see [Troubleshooting](#troubleshooting) if the app won't
open.

On Android, extract the archive in Termux. `uname -m` says `aarch64` for arm64
and `x86_64` for x64. Android 6 needs an `apt-android-5` build of Termux from
its [releases](https://github.com/termux/termux-app/releases).

## Usage

```
apple-event-streamer [options] [-- player options]
```

| Option | What it does |
| --- | --- |
| `--best` | Pick the highest-bitrate SDR + AAC stream (or the `--range`/`--audio` given) without the menu. |
| `--range sdr\|hevc\|hdr10\|dv` | Video type: SDR (H.264), SDR [HEVC], HDR10 or Dolby Vision. Skips that step. |
| `--audio aac\|atmos` | Audio: AAC stereo or Dolby Atmos. Skips that step. |
| `--max-res RES` | Highest resolution to offer, e.g. `1080`, `1440p`, `4k`. |
| `--subs` | Add a subtitles step to the picker. Not with `--best`. |
| `--sub-lang LANGS` | Include these subtitle languages, e.g. `en` or `en,fr`. `en` also matches `en-GB`. |
| `--all-subs` | Include every subtitle language. |
| `--ad` | Use audio description instead of the normal audio. |
| `--asl` | Play the American Sign Language version. |
| `--list` | Print every stream and exit. |
| `--url URL` | Use this master playlist (a URL or a file) instead of finding the event. |
| `--timeout SEC` | Network timeout in seconds. Default 20. |
| `--no-launch` | Write the playlist and print its path, but don't play it. |
| `--keep-playlist [FOLDER]` | Keep the playlist in your Downloads folder, or in `FOLDER`. |
| `--mpv PATH` | Use this mpv. |
| `--mpvnet PATH` | Use this mpv.net. |
| `-v`, `--verbose` | Print details of what's happening. |
| `--version` | Print the version. |
| `-h`, `--help` | Show help. |
| `-- ARGS` | Pass everything after `--` to the player, e.g. `-- --fs --volume=70`. Not with the mpv app. |

Examples:

```sh
apple-event-streamer --best --range dv --audio atmos
apple-event-streamer --best --sub-lang en --ad
apple-event-streamer --best --keep-playlist Videos/Playlists
```

### The picker

Use `↑`/`↓` or `j`/`k` to move, `Enter` or `→` to choose, `←`, `Esc` or
`Backspace` to go back, `1`–`9` to choose directly and `q` to quit. The mouse
works too. On Android, swipe to move, and tap a row to choose it or empty space
to choose the highlighted row.

Video types the event doesn't have (e.g. HDR10 for live events) are greyed
out.

### Players

On Windows the app uses mpv.net if it's on your `PATH`, and mpv if not. On
macOS and Linux it uses mpv. It also looks in common install folders, such as
`/Applications/mpv.app`.

The player runs in the same terminal, and the app exits when it does.

On Android it opens the mpv app and exits once mpv has loaded the stream.
Playback carries on. `--mpv PATH` uses mpv from Termux instead, in the same
terminal.

### Playlists

The playlist is deleted when the player closes. Playlists left behind by a
killed run, or written by `--no-launch` without `--keep-playlist`, are
deleted the next time the app makes a playlist, once they're a day old.

`--keep-playlist` keeps it in your Downloads folder, or in the folder you give
(created if missing). On Android the Downloads folder is `~/storage/downloads`.

The mpv app gets the playlist from `127.0.0.1`, so no file is written unless
you ask for one.

## Troubleshooting

### "mpv was not found on PATH"

Install mpv (`brew install mpv` on macOS), or mpv.net on Windows, or pass
`--mpv PATH`.

### "tar: xz: Cannot exec"

Not every build of Termux comes with xz. Run `pkg install xz-utils`.

### "the mpv app isn't installed"

Install the [mpv app](https://github.com/mpv-android/mpv-android), or pass
`--mpv PATH` to use mpv from Termux.

### "~/storage/downloads isn't available"

`--keep-playlist` uses the link to your Downloads folder that Termux makes. Run
`termux-setup-storage` once, or name a folder.

### macOS won't open the app

The app isn't notarized, so macOS blocks copies downloaded with a browser. Run
`xattr -d com.apple.quarantine apple-event-streamer`, or download with `curl`.

### "the events page has no stream right now"

The stream only appears shortly before the event starts.

### No subtitles

Subtitles are off by default. With `--all-subs` none is selected; press `j` in
mpv, or tap the subtitles button in the mpv app.

### Other problems

Run with `--verbose` and include the output in an
[issue](https://github.com/Mahir007/apple-event-streamer/issues).

## Building

See [CONTRIBUTING.md](CONTRIBUTING.md) for building, CI and releases.
