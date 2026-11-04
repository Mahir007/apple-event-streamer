#!/usr/bin/env bash
# Usage: host.sh BIN_DIR APK_DIR OUT_DIR
# Runs on the CI runner and drives the emulator over adb. Commands go to t.sh
# through a real Termux session; run-as only moves files. Every step runs even
# if an earlier one fails: failures are what this is here to find.
set -u

bin=$1 apks=$2 out=$3
here=$(dirname "$0")
mkdir -p "$out"
home=files/home

tx() { adb shell "run-as com.termux sh -c '$1'"; }
shot() { adb exec-out screencap -p >"$out/$1.png"; }
type_line() {
	adb shell input text "${1// /%s}"
	adb shell input keyevent 66
}
termux_front() { adb shell am start -n com.termux/.app.TermuxActivity >/dev/null; }

# Accepts "built for an older Android" and "isn't responding" dialogs.
dismiss_dialogs() {
	local i xml b nums
	for i in 1 2 3; do
		adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1 || return 0
		xml=$(adb shell cat /sdcard/ui.xml)
		b=$(grep -oE 'resource-id="android:id/(button1|aerr_wait)"[^>]*bounds="[^"]*"' <<<"$xml" | grep -oE '\[[0-9]+,[0-9]+\]\[[0-9]+,[0-9]+\]' | head -1)
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
	local name=$1 limit=${2:-150} attempt i
	for attempt in 1 2; do
		tx "rm -f $home/done.$name"
		type_line "sh t.sh $name"
		for ((i = 0; i < limit; i++)); do
			if tx "test -e $home/done.$name"; then
				tx "cat $home/r.$name.txt" >"$out/r.$name.txt"
				return 0
			fi
			sleep 1
		done
		echo "step $name timed out (attempt $attempt)"
		shot "timeout-$name-$attempt"
		dismiss_dialogs
		termux_front
		sleep 2
	done
	return 1
}

# play_case NAME SECONDS: starts the app from Termux, lets it run and records
# the outcome. Each case starts with the app stopped and the logs clear.
play_case() {
	local name=$1 seconds=$2
	echo "--- play $name"
	adb shell am force-stop is.xyz.mpv
	termux_front
	sleep 2
	adb logcat -c
	tx ": >$home/serve.log"
	run_t "play-$name"
	sleep "$seconds"
	# A tap brings up the app's controls, with the position, for the screenshot.
	adb shell input tap 500 500
	sleep 1
	shot "play-$name"
	adb logcat -d >"$out/logcat-$name.txt"
	tx "cat $home/serve.log" >"$out/serve-$name.txt"
	adb shell dumpsys media_session | grep -E 'state=PlaybackState|state=' | head -2 >"$out/session-$name.txt"
	adb shell dumpsys activity activities | grep -E 'mResumedActivity|topResumedActivity' | head -2 >>"$out/session-$name.txt"
}

adb shell wm dismiss-keyguard
adb shell svc power stayon true

adb install -g "$apks/termux.apk" 2>&1 | tee "$out/install-termux.txt"
adb install -g "$apks/mpv.apk" 2>&1 | tee "$out/install-mpv.txt"

termux_front
for ((i = 0; i < 240; i++)); do
	tx "test -x files/usr/bin/bash && test -x files/usr/bin/am" && break
	[ $((i % 30)) = 29 ] && dismiss_dialogs
	sleep 1
done
echo "Termux bootstrap ready after ${i}s"
sleep 10
dismiss_dialogs
shot termux-first

adb push "$here/t.sh" /data/local/tmp/ >/dev/null
adb push "$bin"/. /data/local/tmp/ >/dev/null
adb shell chmod 755 '/data/local/tmp/*'
tx "cp /data/local/tmp/t.sh /data/local/tmp/aes-* /data/local/tmp/serve $home/ && chmod 755 $home/aes-* $home/serve"
tx "ls -l $home/" | tee "$out/files.txt"

run_t basics
run_t linux
run_t android 240
run_t playlists 90
run_t serve 30
shot termux-after-basics

# Raw mouse bytes for each tracking mode, with two taps while it reads.
for mode in 1003 1002; do
	termux_front
	sleep 1
	tx "rm -f $home/done.mouse-$mode"
	type_line "sh t.sh mouse-$mode"
	sleep 5
	adb shell input tap 400 600
	sleep 1
	adb shell input tap 400 700
	for ((i = 0; i < 30; i++)); do
		tx "test -e $home/done.mouse-$mode" && break
		sleep 1
	done
	tx "cat $home/r.mouse-$mode.txt" >"$out/r.mouse-$mode.txt"
done

# The real picker: sweep taps down the screen, then press q if it's still open.
for pick in pick-allmotion pick-cell; do
	termux_front
	sleep 1
	tx "rm -f $home/done.$pick"
	type_line "sh t.sh $pick"
	sleep 12
	shot "$pick-before"
	for _ in 1 2 3 4 5; do
		for y in 150 250 350 450 550 650 750 850 950 1050; do
			tx "test -e $home/done.$pick" && break 2
			adb shell input tap 300 "$y"
			sleep 0.2
		done
	done
	shot "$pick-after"
	tx "test -e $home/done.$pick" || adb shell input text q
	for ((i = 0; i < 15; i++)); do
		tx "test -e $home/done.$pick" && break
		sleep 1
	done
	tx "cat $home/r.$pick.txt" >"$out/r.$pick.txt"
done

termux_front
sleep 1
run_t am-missing 30

tx "cat $home/www/plain.m3u8" >"$out/plain.m3u8"
adb push "$out/plain.m3u8" /sdcard/Download/plain.m3u8 >/dev/null

play_case http 30
play_case http-sysam 25
play_case http-nomime 25
play_case http-nocomponent 25
play_case data 25
play_case file-private 25
play_case file-sdcard 25
play_case content 25
play_case subs 25

# Does playback outlive the server, with Termux in the background all along?
play_case http 20
shot play-http-before-kill
adb shell dumpsys media_session | grep -E 'state=' | head -1 >"$out/position-before-kill.txt"
tx "kill \$(cat $home/serve.pid)"
sleep 30
adb shell input tap 500 500
sleep 1
shot play-http-after-kill
adb shell dumpsys media_session | grep -E 'state=' | head -1 >"$out/position-after-kill.txt"
tx "cat $home/serve.log" >"$out/serve-kill.txt"
adb shell am force-stop is.xyz.mpv
termux_front
sleep 2
run_t serve 30

# The fix users can make for subtitles: one line in the app's mpv.conf.
adb root
sleep 3
adb wait-for-device
conf=/data/data/is.xyz.mpv/files/mpv.conf
adb shell "echo demuxer-lavf-o-add=strict=experimental >$conf"
owner=$(adb shell stat -c %u:%g /data/data/is.xyz.mpv/files)
adb shell "chown $owner $conf; restorecon $conf; ls -lZ $conf" | tee "$out/mpv-conf.txt"
play_case subs-conf 25

# What each case left, for the job summary.
{
	echo "## Android $(adb shell getprop ro.build.version.release) (API $(adb shell getprop ro.build.version.sdk)), page size $(adb shell getconf PAGESIZE)"
	for f in "$out"/r.*.txt; do
		echo
		echo "### $(basename "$f" .txt)"
		echo '```'
		cut -c1-200 "$f"
		echo '```'
	done
	for f in "$out"/logcat-*.txt; do
		n=$(basename "$f" .txt)
		n=${n#logcat-}
		echo
		echo "### play $n"
		echo '```'
		echo "requests: $(wc -l <"$out/serve-$n.txt")"
		cut -c1-140 "$out/serve-$n.txt" | head -6
		cut -c1-160 "$out/session-$n.txt"
		grep -E ' mpv *:| MPVActivity *:|is.xyz.mpv' "$f" | grep -E 'Video +--vid|Audio +--aid|Subs +--sid|whitelist|support the subtitle|Not detecting|Failed|failed to|unknown scheme|Permission denied|Exiting|No file given|Could not|Playing:' | sed -E 's/^[0-9-]+ [0-9:.]+ +[0-9]+ +[0-9]+ //' | cut -c1-170 | sort -u | head -14
		echo '```'
	done
} >"$out/summary.md"
echo "probe finished"
