#!/usr/bin/env bash
# Usage: docker.sh IMAGE BINARY
# Runs an Android binary in termux-docker, with increasing privileges, to find
# the least a check job needs. Prints a Markdown report.
set -u

image=$1 binary=$2
chmod -R a+rX probe-bin
docker pull -q "$image" >/dev/null
echo "### $image, $binary"
for flags in "" "--security-opt seccomp=unconfined" "--privileged"; do
	echo
	echo "With docker flags '$flags':"
	echo '```'
	# shellcheck disable=SC2086 # flags is a list of words
	timeout 120 docker run --rm $flags -v "$PWD/probe-bin:/mnt/bin:ro" "$image" \
		sh -c "uname -m; /mnt/bin/$binary --version; echo \"[exit \$?]\"; /mnt/bin/$binary --list 2>&1 | head -3; echo done" 2>&1 | cut -c1-160
	echo "[docker exit ${PIPESTATUS[0]}]"
	echo '```'
done
