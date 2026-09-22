package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const directoryFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

type binding struct {
	Target, Path string
	Create       bool
	UID, GID     *int
	Mode         *string
}

var numeric = regexp.MustCompile(`^[0-9]+$`)

func openDirectory(path string, create bool) (int, error) {
	if !strings.HasPrefix(path, "/") {
		return -1, errors.New("expected an absolute directory path")
	}

	components := strings.Split(path, "/")
	for _, p := range components {
		if p == "." || p == ".." {
			return -1, errors.New("dot components are forbidden")
		}
	}

	fd, err := unix.Open("/", directoryFlags, 0)
	if err != nil {
		return -1, err
	}

	for _, p := range components {
		if p == "" {
			continue
		}

		child, err := unix.Openat(fd, p, directoryFlags, 0)
		if errors.Is(err, unix.ENOENT) && create {
			err = unix.Mkdirat(fd, p, 0755)
			if err == nil || errors.Is(err, unix.EEXIST) {
				child, err = unix.Openat(fd, p, directoryFlags, 0)
			}
		}

		unix.Close(fd)
		if err != nil {
			return -1, err
		}

		fd = child
	}

	return fd, nil
}

func unpin(fd int) error {
	entries, err := os.ReadDir(fmt.Sprintf("/proc/self/fd/%d", fd))
	if err != nil {
		return err
	}

	var failures []error
	for _, entry := range entries {
		n := entry.Name()
		if !numeric.MatchString(n) {
			return fmt.Errorf("unexpected bind staging entry: %s", n)
		}

		err = unix.Unmount(fmt.Sprintf("/proc/self/fd/%d/%s", fd, n), unix.MNT_DETACH)
		if err != nil && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOENT) {
			failures = append(failures, err)
			continue
		}

		err = unix.Unlinkat(fd, n, unix.AT_REMOVEDIR)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			failures = append(failures, err)
		}
	}

	return errors.Join(failures...)
}

func pin(b binding, fd int) error {
	if err := unix.Mkdirat(fd, b.Target, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}

	target, err := unix.Openat(fd, b.Target, directoryFlags, 0)
	if err != nil {
		return err
	}

	unix.Close(target)
	source, err := openDirectory(b.Path, b.Create)
	if err != nil {
		return err
	}
	defer unix.Close(source)

	uid, gid := -1, -1
	if b.UID != nil {
		uid = *b.UID
	}

	if b.GID != nil {
		gid = *b.GID
	}

	if uid != -1 || gid != -1 {
		if err = unix.Fchown(source, uid, gid); err != nil {
			return err
		}
	}

	if b.Mode != nil {
		mode, err := strconv.ParseUint(*b.Mode, 8, 32)
		if err != nil {
			return err
		}

		if err = unix.Fchmod(source, uint32(mode)); err != nil {
			return err
		}
	}

	path := fmt.Sprintf("/proc/self/fd/%d/%s", fd, b.Target)
	if err = unix.Mount(fmt.Sprintf("/proc/self/fd/%d", source), path, "", unix.MS_BIND, ""); err != nil {
		return err
	}

	return unix.Mount("", path, "", unix.MS_PRIVATE, "")
}

func run(args []string) error {
	if len(args) != 3 || (args[0] != "pin" && args[0] != "unpin") {
		return errors.New("usage: pinned-bind-sources pin|unpin MANIFEST RUNTIME_DIRECTORY")
	}

	data, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}

	var bindings []binding
	if err = json.Unmarshal(data, &bindings); err != nil {
		return err
	}

	seen := make(map[string]bool, len(bindings))
	for _, b := range bindings {
		if !numeric.MatchString(b.Target) {
			return errors.New("bind staging names must be numeric")
		}
		if args[0] == "pin" && seen[b.Target] {
			return fmt.Errorf("duplicate bind staging name: %s", b.Target)
		}
		seen[b.Target] = true
	}

	fd, err := openDirectory(args[2], false)
	if err != nil {
		if errors.Is(err, unix.ENOENT) && args[0] == "unpin" {
			return nil
		}

		return err
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}

	if int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 {
		return errors.New("bind staging directory must be private and owned by the service user")
	}

	if err = unpin(fd); err != nil {
		return err
	}

	if args[0] == "pin" {
		for _, b := range bindings {
			if err = pin(b, fd); err != nil {
				return err
			}
		}

		return nil
	}

	return os.Remove(args[2])
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
