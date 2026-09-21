package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSourceDirectoryRejectsSymlinkAncestors(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{link, filepath.Join(link, "child"), root + "/../outside"} {
		fd, err := openDirectory(path, true)
		if err == nil {
			unix.Close(fd)
			t.Fatalf("accepted unsafe source %q", path)
		}
	}

	if entries, err := os.ReadDir(real); err != nil || len(entries) != 0 {
		t.Fatalf("created directories through symlink: %v, %v", entries, err)
	}
}

func TestSourceDescriptorSurvivesPathReplacement(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "nested")
	fd, err := openDirectory(source, true)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)

	if err = os.WriteFile(filepath.Join(source, "data"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	if err = os.Rename(filepath.Join(root, "source"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}

	if err = os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}

	if err = os.WriteFile(filepath.Join(source, "data"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fd/%d/data", fd))
	if err != nil || string(data) != "original" {
		t.Fatalf("held source followed path replacement: %q, %v", data, err)
	}
}
