package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveBinaryPrefersPlatformBinary(t *testing.T) {
	root := t.TempDir()
	generic := filepath.Join(root, "monitor")
	platform := generic + "-" + runtime.GOOS + "-" + runtime.GOARCH
	if err := os.WriteFile(generic, []byte("generic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(platform, []byte("platform"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveBinary(root, "monitor")
	if err != nil {
		t.Fatal(err)
	}
	if got != platform {
		t.Fatalf("resolveBinary selected %q, want %q", got, platform)
	}
}
