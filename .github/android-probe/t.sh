#!/data/data/com.termux/files/usr/bin/sh
# Runs inside a real Termux terminal session, typed by host.sh, so everything
# here has the app's uid, SELinux domain and seccomp filter.
cd "$HOME" || exit 1
name=$1
exec >"r.$name.txt" 2>&1
trap ': >"done.$name"' EXIT

# Show a command, up to 16 lines of its output, and its exit code.
try() {
	echo "\$ $*"
	"$@" >try.out 2>&1
	rc=$?
	head -n 16 try.out | cut -c1-220
	echo "[exit $rc]"
	echo
}

M=is.xyz.mpv/.MPVActivity
V=android.intent.action.VIEW
H=application/vnd.apple.mpegurl
U=http://127.0.0.1:18765

# Start the mpv app from this session and time how long am takes.
play() {
	s=$(date +%s%N)
	echo "\$ am start $*" | cut -c1-200
	"${AM:-am}" start -a $V "$@" 2>&1 | cut -c1-300
	e=$(date +%s%N)
	echo "[am took $(((e - s) / 1000000)) ms]"
}

case $name in
basics)
	try id
	try uname -a
	try getprop ro.build.version.release
	try getprop ro.build.version.sdk
	try getprop ro.product.cpu.abilist
	try getconf PAGESIZE
	try grep Seccomp /proc/self/status
	try cat /proc/self/attr/current
	try sh -c 'command -v am; ls -l "$(command -v am)"; head -5 "$(command -v am)"'
	try ls -l /etc/resolv.conf "$PREFIX/etc/resolv.conf" "$PREFIX/etc/tls/cert.pem"
	try sh -c 'echo HOME=$HOME TMPDIR=$TMPDIR TERM=$TERM TERMUX_VERSION=$TERMUX_VERSION TERMUX__USER_ID=$TERMUX__USER_ID LD_PRELOAD=$LD_PRELOAD'
	;;
linux)
	# The Linux release binary. exec.LookPath is what would call faccessat2.
	try ./aes-linux --version
	try ./aes-linux --list
	try ./aes-linux --mpv /system/bin/true --best --url /nonexistent
	;;
android)
	try ./aes-android --version
	try ./aes-android --list
	# Go's own DNS resolver, which is all a build without cgo has.
	try env GODEBUG=netdns=go ./aes-android --list
	try ./aes-android --mpv /system/bin/true --best --max-res 480
	try ./aes-android --mpv "$PREFIX/bin/true" --best --max-res 480
	try ./aes-android --best --max-res 480 --keep-playlist --no-launch
	# Whether an arm64 binary runs here through translation.
	try ./aes-android-arm64 --version
	;;
playlists)
	mkdir -p www
	p=$(./aes-android --best --max-res 480 --no-launch)
	echo "playlist written to $p"
	cp "$p" www/plain.m3u8 && rm "$p"
	p=$(./aes-android --best --max-res 480 --sub-lang en --no-launch)
	cp "$p" www/subs.m3u8 && rm "$p"
	try wc -c www/plain.m3u8 www/subs.m3u8
	;;
goexec)
	# Starting things from Go: a raw execve, with no termux-exec to help.
	try ./gx /system/bin/true
	try ./gx "$PREFIX/bin/true"
	try ./gx am --version
	try ./gx /system/bin/sh -c 'exec am --version'
	;;
launcher)
	mkdir -p inst/lib
	cp aes-android inst/lib/apple-event-streamer-amd64
	cp launch inst/apple-event-streamer
	chmod 755 inst/lib/apple-event-streamer-amd64 inst/apple-event-streamer
	try ./inst/apple-event-streamer --version
	ln -sf "$HOME/inst/apple-event-streamer" "$PREFIX/bin/aes-launch"
	try aes-launch --version
	;;
serve)
	./serve www 18765 >>serve.log 2>&1 &
	echo $! >serve.pid
	echo "serving, pid $(cat serve.pid)"
	;;
mouse-1003 | mouse-1002)
	mode=${name#mouse-}
	stty raw -echo </dev/tty
	printf '\033[?%sh\033[?1006h' "$mode" >/dev/tty
	timeout --foreground 15 dd bs=1 count=64 of="bytes.$mode" </dev/tty 2>/dev/null
	printf '\033[?%sl\033[?1006l' "$mode" >/dev/tty
	stty sane </dev/tty
	echo "bytes the terminal sent for taps in mode $mode:"
	od -c "bytes.$mode" | head -5
	;;
pick-allmotion | pick-cell)
	# The real picker, with stderr on the terminal. Taps that work walk it to
	# the end, which prints a path and exits 0. Otherwise q exits 130.
	case $name in
	pick-allmotion) b=./aes-android ;;
	*) b=./aes-android-cell ;;
	esac
	"$b" --no-launch </dev/tty 2>/dev/tty
	echo "[exit $?]"
	;;
play-http | play-http-fresh) play -t $H -n $M -d "$U/plain.m3u8" --es title "Apple Event · SDR" ;;
play-http-sysam) AM=/system/bin/am play -t $H -n $M -d "$U/plain.m3u8" ;;
play-http-nomime) play -n $M -d "$U/plain.m3u8" ;;
play-http-nocomponent) play -t video/any -p is.xyz.mpv -d "$U/plain.m3u8" ;;
play-data) play -n $M -d "data:$H;base64,$(base64 -w0 www/plain.m3u8)" ;;
play-file-private) play -t $H -n $M -d "file://$HOME/www/plain.m3u8" ;;
play-file-sdcard | play-file-sdcard-noperm) play -t $H -n $M -d "file:///sdcard/Download/plain.m3u8" ;;
play-content) play -t $H -n $M -d "content://com.termux.files$HOME/www/plain.m3u8" --grant-read-uri-permission ;;
play-subs | play-subs-conf) play -t $H -n $M -d "$U/subs.m3u8" ;;
am-missing)
	play -t $H -n is.xyz.mpv.nope/is.xyz.mpv.MPVActivity -d "$U/plain.m3u8"
	am start -a $V -n is.xyz.mpv.nope/is.xyz.mpv.MPVActivity -d "$U/plain.m3u8" >/dev/null 2>&1
	echo "[am exit code with a missing app: $?]"
	;;
*)
	echo "unknown step $name"
	;;
esac
