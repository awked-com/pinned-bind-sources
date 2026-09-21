package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMountSurvivesSourceReplacement(t *testing.T) {
	if os.Getenv("PINNED_BIND_MOUNT_TESTS") != "1" {
		t.Skip("run in an isolated mount namespace with PINNED_BIND_MOUNT_TESTS=1")
	}
	root := t.TempDir()
	source, runtime := filepath.Join(root, "source"), filepath.Join(root, "runtime")
	for _, path := range []string{source, runtime} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := openDirectory(runtime, false)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	defer func() {
		if err := unpin(fd); err != nil {
			t.Error(err)
		}
	}()
	if err := pin(binding{Target: "0", Path: source}, fd); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(runtime, "0", "data"))
	if err != nil || string(data) != "original" {
		t.Fatalf("pinned content: %q, %v", data, err)
	}
	if err := unpin(fd); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil || len(entries) != 0 {
		t.Fatalf("cleanup: %v, %v", entries, err)
	}
}
