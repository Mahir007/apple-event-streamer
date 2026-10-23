package player

import (
	"slices"
	"testing"
)

func TestArgs(t *testing.T) {
	mpv := &Player{Kind: MPV, Path: "mpv"}
	got := mpv.Args("/tmp/apple-event-x.m3u8", Options{Extra: []string{"--fs"}})
	want := []string{`--demuxer-lavf-o=protocol_whitelist="file,crypto,data,https,http,tls,tcp"`, "--fs", "/tmp/apple-event-x.m3u8"}
	if !slices.Equal(got, want) {
		t.Errorf("mpv args = %q", got)
	}

	net := &Player{Kind: MPVNet, Path: "mpvnet"}
	got = net.Args("p.m3u8", Options{Subtitles: true, SubLangs: []string{"en", "fr"}})
	want = []string{`--demuxer-lavf-o=protocol_whitelist="file,crypto,data,https,http,tls,tcp",strict=experimental`, "--process-instance=multi", "--slang=en,fr", "p.m3u8"}
	if !slices.Equal(got, want) {
		t.Errorf("mpvnet args = %q", got)
	}
}
