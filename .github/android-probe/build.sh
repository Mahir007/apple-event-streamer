#!/usr/bin/env bash
# Builds everything the probe runs into the folder $1.
set -euo pipefail

out=$(realpath -m "$1")
mkdir -p "$out"
bin=$ANDROID_HOME/ndk/$NDK_VERSION/toolchains/llvm/prebuilt/linux-x86_64/bin
amd64_cc=$bin/x86_64-linux-android24-clang
arm64_cc=$bin/aarch64-linux-android24-clang
flags=(-trimpath -tags nethttpomithttp2 -ldflags '-s -w -X main.version=v0.0.0-probe')

# What releases ship for Linux today.
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build "${flags[@]}" -o "$out/aes-linux" .

# The candidates: the same source for Android, through the NDK.
GOOS=android GOARCH=amd64 CGO_ENABLED=1 CC=$amd64_cc go build "${flags[@]}" -o "$out/aes-android" .
GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC=$arm64_cc go build "${flags[@]}" -o "$out/aes-android-arm64" .

# The x86_64 candidate with the picker asking for mouse mode 1002, not 1003.
sed -i 's/tea.MouseModeAllMotion/tea.MouseModeCellMotion/' internal/picker/picker.go
GOOS=android GOARCH=amd64 CGO_ENABLED=1 CC=$amd64_cc go build "${flags[@]}" -o "$out/aes-android-cell" .
git checkout -- internal/picker/picker.go

GOOS=android GOARCH=amd64 CGO_ENABLED=1 CC=$amd64_cc go build -trimpath -o "$out/serve" .github/android-probe/serve/main.go

ls -l "$out"
