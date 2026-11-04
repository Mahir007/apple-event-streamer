#!/usr/bin/env bash
# Usage: host.sh ARCHIVE APK_DIR OUT_DIR
# End-to-end test of a real Android archive on an emulator, driven over adb.
# Commands go to t.sh through a real Termux session; run-as only moves files.
# Exits non-zero if anything isn't as it should be.
set -u

archive=$1 apks=$2 out=$3
here=$(dirname "$0")
mkdir -p "$out"
home=files/home
failed=0

real_adb=$(command -v adb)
adb() { timeout "${ADB_TIMEOUT:-60}" "$real_adb" "$@"; }

# Android 6's adb doesn't pass exit codes or binary-safe output, so this strips
# carriage returns and tx_ok asks the device to say whether a test passed.
tx() { adb shell "run-as com.termux sh -c '$1'" | tr -d '\r'; }
tx_ok() { [ "$(tx "$1 && echo yes")" = yes ]; }
prop() { adb shell getprop "$1" | tr -d '\r'; }
shot() { adb exec-out screencap -p >"$out/$1.png"; }
type_line() {
	adb shell input text "${1// /%s}"
	adb shell input keyevent 66
}
termux_front() { adb shell am start -n com.termux/.app.TermuxActivity >/dev/null; }

report() { echo "$1" | tee -a "$out/summary.md"; }
pass() { report "- ok: $1"; }
fail() {
	report "- FAILED: $1"
	failed=1
}
finish() {
	if [ "$failed" = 0 ]; then report "All passed."; else report "Something failed."; fi
	exit "$failed"
}
die() {
	fail "$1"
	shot died
	finish
}
expect() { # FILE REGEX DESCRIPTION
	if grep -qE -- "$2" "$1" 2>/dev/null; then pass "$3"; else fail "$3: no match for '$2' in $(basename "$1")"; fi
}
refuse() { # FILE REGEX DESCRIPTION
	if grep -qE -- "$2" "$1" 2>/dev/null; then fail "$3: '$2' in $(basename "$1")"; else pass "$3"; fi
}

# Android 6 encrypts /data on first boot. sys.boot_completed is set before that,
# then the framework restarts on the real /data, and anything installed before
# then is gone. Later versions don't do this.
wait_for_boot() {
	local i sdk
	for ((i = 0; i < 120; i++)); do
		sdk=$(prop ro.build.version.sdk)
		if { [ "${sdk:-0}" -gt 23 ] || [ "$(prop vold.decrypt)" = trigger_restart_framework ]; } &&
			[ "$(prop sys.boot_completed)" = 1 ] && adb shell pm path android 2>/dev/null | grep -q package:; then
			echo "boot finished after $((i * 5))s, API $sdk"
			return 0
		fi
		sleep 5
	done
	die "the system never finished booting"
}

# Accepts the dialogs some Android versions show when an app starts.
dismiss_dialogs() {
	local i xml b nums
	for i in 1 2 3; do
		adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1 || return 0
		xml=$(adb shell cat /sdcard/ui.xml | tr -d '\r')
		b=$(grep -oE "(resource-id=\"android:id/(button1|aerr_wait)\"|text=\"Don.t Show Again\")[^>]*bounds=\"[^\"]*\"" <<<"$xml" | grep -oE '\[[0-9]+,[0-9]+\]\[[0-9]+,[0-9]+\]' | head -1)
		[ -n "$b" ] || return 0
		mapfile -t nums < <(grep -oE '[0-9]+' <<<"$b")
		echo "dismissing a dialog at $b"
		adb shell input tap $(((nums[0] + nums[2]) / 2)) $(((nums[1] + nums[3]) / 2))
		sleep 2
	done
}

# run_t STEP [SECONDS]: types "sh t.sh STEP" and waits for it, retrying once in
# case the session wasn't ready for the first attempt.
run_t() {
	local name=$1 limit=${2:-90} attempt i
	for attempt in 1 2; do
		termux_front
		sleep 2
		tx "rm -f $home/done.$name"
		type_line "sh t.sh $name"
		for ((i = 0; i < limit; i++)); do
			if tx_ok "test -e $home/done.$name"; then
				tx "cat $home/r.$name.txt" >"$out/r.$name.txt"
				return 0
			fi
			sleep 1
		done
		echo "step $name timed out (attempt $attempt)"
		shot "timeout-$name-$attempt"
		dismiss_dialogs
	done
	fail "step $name never finished"
	return 1
}

position() {
	adb shell dumpsys media_session | tr -d '\r' | grep -m1 -oE 'state=PlaybackState \{state=[^,]+, position=[0-9]+' | grep -oE '[0-9]+$'
}

# play_case STEP: stops the mpv app, runs STEP, then checks the app is playing.
play_case() {
	local name=$1 a b
	adb shell am force-stop is.xyz.mpv
	adb logcat -c
	run_t "$name"
	expect "$out/r.$name.txt" '\[exit 0\]' "$name: the CLI exits 0"
	sleep 10
	a=$(position)
	sleep 10
	b=$(position)
	if [ "${b:-0}" -gt "${a:-0}" ]; then pass "$name: playback advances, ${a}ms to ${b}ms"; else fail "$name: playback stuck, ${a:-none} then ${b:-none}"; fi
	shot "$name"
	adb logcat -d -s mpv | tr -d '\r' | sed -E 's/^[0-9-]+ [0-9:.]+ +[0-9]+ +[0-9]+ //' >"$out/mpv-$name.txt"
	expect "$out/mpv-$name.txt" 'Video +--vid' "$name: video track"
	expect "$out/mpv-$name.txt" 'Audio +--aid' "$name: audio track"
	expect "$out/mpv-$name.txt" 'Opening http://127\.0\.0\.1' "$name: playlist from 127.0.0.1"
}

report "## Android $(prop ro.build.version.release), API $(prop ro.build.version.sdk)"

wait_for_boot
adb shell wm dismiss-keyguard
adb shell svc power stayon true

ADB_TIMEOUT=240 adb install -g "$apks/termux.apk" 2>&1 | tail -1
# Never opened, and nothing granted.
ADB_TIMEOUT=240 adb install "$apks/mpv.apk" 2>&1 | tail -1
adb shell pm path com.termux | grep -q package: || die "Termux didn't install"
adb shell pm path is.xyz.mpv | grep -q package: || die "the mpv app didn't install"

termux_front
for ((i = 0; i < 240; i++)); do
	tx_ok "test -e files/usr/bin/bash" && break
	[ $((i % 30)) = 29 ] && dismiss_dialogs
	sleep 1
done
tx_ok "test -e files/usr/bin/bash" || die "Termux never finished installing"
sleep 10
dismiss_dialogs

# Wait for the network too: an emulator can finish booting before it has any.
for ((i = 0; i < 60; i++)); do
	adb shell ping -c1 -W3 www.apple.com >/dev/null 2>&1 && break
	sleep 2
done

adb push "$here/t.sh" /data/local/tmp/t.sh >/dev/null
adb push "$archive" /data/local/tmp/archive.tar.xz >/dev/null
adb shell chmod 644 /data/local/tmp/t.sh /data/local/tmp/archive.tar.xz
tx "cp /data/local/tmp/t.sh /data/local/tmp/archive.tar.xz $home/"

run_t basics 60
cat "$out/r.basics.txt"
expect "$out/r.basics.txt" 'Seccomp:[[:space:]]+[0-9]' "basics: ran in a Termux session"

run_t install 60
refuse "$out/r.install.txt" '\[exit [1-9]' "install: extracted and copied with Termux's tools"

run_t version 30
expect "$out/r.version.txt" '^apple-event-streamer v0\.0\.0-probe \(android/amd64\)$' "version: right version and platform"
refuse "$out/r.version.txt" 'WARNING' "version: no linker warning"

run_t list 60
expect "$out/r.list.txt" '^RANGE' "list: fetched the stream list over the network"
expect "$out/r.list.txt" '\[exit 0\]' "list: exits 0"

# The emulator's hardware decoder on Android 11 hangs mpv, whatever the source.
if [ "${MPV_HWDEC_NO:-}" = 1 ]; then
	adb root >/dev/null
	sleep 4
	adb wait-for-device
	conf=/data/data/is.xyz.mpv/files/mpv.conf
	adb shell "mkdir -p /data/data/is.xyz.mpv/files; echo hwdec=no >$conf"
	owner=$(adb shell stat -c %u:%g /data/data/is.xyz.mpv | tr -d '\r')
	adb shell "chown -R $owner /data/data/is.xyz.mpv/files; restorecon -R /data/data/is.xyz.mpv/files"
fi

play_case play-fresh
adb shell dumpsys package is.xyz.mpv | tr -d '\r' | grep -E 'READ_[A-Z_]+: granted=true' >"$out/granted.txt"
refuse "$out/granted.txt" 'granted=true' "play-fresh: the mpv app had no storage permission"
refuse "$out/r.play-fresh.txt" 'playlist' "play-fresh: wrote no playlist file"

play_case subs
expect "$out/mpv-subs.txt" 'Subs +--sid' "subs: subtitle track, with no mpv.conf"

play_case all-subs
grep -E 'Subs +--sid' "$out/mpv-all-subs.txt" | cut -c1-100 | head -4 | tee -a "$out/summary.md"

run_t args 30
expect "$out/r.args.txt" "can't take player options" "args: refused for the mpv app"
expect "$out/r.args.txt" '\[exit 2\]' "args: exits 2"

run_t keep-missing 60
expect "$out/r.keep-missing.txt" 'termux-setup-storage' "keep-missing: says to run termux-setup-storage"
expect "$out/r.keep-missing.txt" '\[exit 1\]' "keep-missing: exits 1"
expect "$out/r.keep-missing.txt" "No such file" "keep-missing: didn't create the folder"

play_case keep
expect "$out/r.keep.txt" 'Playlist kept at .*/storage/downloads/' "keep: says where the playlist went"
expect "$out/r.keep.txt" '^apple-event-[0-9a-f]+\.m3u8$' "keep: the file is there"

run_t mpv-flag 60
expect "$out/r.mpv-flag.txt" '\[exit 0\]' "mpv-flag: ran a program from Termux with --mpv"

adb shell am force-stop is.xyz.mpv
ADB_TIMEOUT=120 adb uninstall is.xyz.mpv
run_t not-installed 60
expect "$out/r.not-installed.txt" "the mpv app isn't installed" "not-installed: says so"
expect "$out/r.not-installed.txt" '\[exit 1\]' "not-installed: exits 1"

{
	for f in "$out"/r.*.txt; do
		echo
		echo "### $(basename "$f" .txt)"
		echo '```'
		cut -c1-200 "$f" | head -24
		echo '```'
	done
} >>"$out/summary.md"
finish
