#!/data/data/com.termux/files/usr/bin/sh
# Runs inside a real Termux terminal session, typed by host.sh, so everything
# here has the app's uid, SELinux domain and seccomp filter.
cd "$HOME" || exit 1
name=$1
exec >"r.$name.txt" 2>&1
trap ': >"done.$name"' EXIT

aes=apple-event-streamer

# Show a command, its output and its exit code.
run() {
	echo "\$ $*"
	"$@"
	echo "[exit $?]"
}

case $name in
basics)
	uname -m
	echo "API $(getprop ro.build.version.sdk), $(grep Seccomp: /proc/self/status)"
	;;
install)
	# Not every build of Termux comes with xz. The README says to do this.
	command -v xz >/dev/null || run pkg install -y xz-utils
	run tar -xf archive.tar.xz
	run cp apple-event-streamer_v0.0.0-probe_android-amd64/apple-event-streamer "$PREFIX/bin/"
	;;
version) run $aes --version ;;
list) run $aes --list ;;
play | play-fresh) run $aes --best --max-res 480 ;;
subs) run $aes --best --max-res 480 --sub-lang en ;;
all-subs) run $aes --best --max-res 480 --all-subs ;;
args) run $aes --best --max-res 480 -- --fs ;;
keep-missing)
	run $aes --best --max-res 480 --keep-playlist --no-launch
	ls -d storage
	;;
keep)
	# A folder stands in for the link termux-setup-storage makes.
	mkdir -p storage/downloads
	run $aes --best --max-res 480 --keep-playlist
	ls storage/downloads
	;;
mpv-flag) run $aes --best --max-res 480 --mpv "$PREFIX/bin/true" ;;
not-installed) run $aes --best --max-res 480 ;;
*) echo "unknown step $name" ;;
esac
