//go:build unix

package player

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// script writes an executable shell script standing in for the player.
func script(t *testing.T, body string) *Player {
	t.Helper()
	p := filepath.Join(t.TempDir(), "player")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Player{Kind: MPV, Path: p}
}

func TestRunPassesExitCodeAndCleansUp(t *testing.T) {
	for _, code := range []int{0, 1, 4} {
		cleaned := false
		got, err := script(t, "exit "+string(rune('0'+code))).Run(nil, func() { cleaned = true })
		if err != nil || got != code || !cleaned {
			t.Errorf("exit %d: got %d, %v, cleaned=%v", code, got, err, cleaned)
		}
	}
}

func TestRunCleansUpWhenStartFails(t *testing.T) {
	cleaned := false
	p := &Player{Kind: MPV, Path: filepath.Join(t.TempDir(), "missing")}
	if _, err := p.Run(nil, func() { cleaned = true }); err == nil || !cleaned {
		t.Fatalf("err=%v cleaned=%v", err, cleaned)
	}
}

func TestRunKillsPlayerIgnoringInterrupt(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the kill timeout")
	}
	p := script(t, `trap "" INT TERM; exec sleep 30`)
	go func() {
		time.Sleep(300 * time.Millisecond)
		syscall.Kill(os.Getpid(), syscall.SIGINT)
	}()
	start := time.Now()
	code, err := p.Run(nil, func() {})
	if err != nil || code != 130 {
		t.Fatalf("got %d, %v; want 130", code, err)
	}
	if d := time.Since(start); d < 3*time.Second || d > 10*time.Second {
		t.Fatalf("returned after %v, want the ~3s grace period", d)
	}
}
