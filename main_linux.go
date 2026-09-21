package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"regexp"
	"strconv"
	"strings"
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

	for _, p := range strings.Split(path, "/") {
		if p == "." || p == ".." {
			return -1, errors.New("dot components are forbidden")
		}
	}

	fd, e := unix.Open("/", directoryFlags, 0)
	if e != nil {
		return -1, e
	}

	for _, p := range strings.Split(path, "/") {
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
	entries, e := os.ReadDir(fmt.Sprintf("/proc/self/fd/%d", fd))
	if e != nil {
		return e
	}

	var failures []error
	for _, entry := range entries {
		n := entry.Name()
		if !numeric.MatchString(n) {
			return fmt.Errorf("unexpected bind staging entry: %s", n)
		}

		e = unix.Unmount(fmt.Sprintf("/proc/self/fd/%d/%s", fd, n), unix.MNT_DETACH)
		if e != nil && !errors.Is(e, unix.EINVAL) && !errors.Is(e, unix.ENOENT) {
			failures = append(failures, e)
			continue
		}

		e = unix.Unlinkat(fd, n, unix.AT_REMOVEDIR)
		if e != nil && !errors.Is(e, unix.ENOENT) {
			failures = append(failures, e)
		}
	}

	return errors.Join(failures...)
}

func pin(b binding, fd int) error {
	if e := unix.Mkdirat(fd, b.Target, 0700); e != nil && !errors.Is(e, unix.EEXIST) {
		return e
	}

	target, e := unix.Openat(fd, b.Target, directoryFlags, 0)
	if e != nil {
		return e
	}

	unix.Close(target)
	source, e := openDirectory(b.Path, b.Create)
	if e != nil {
		return e
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
		if e = unix.Fchown(source, uid, gid); e != nil {
			return e
		}
	}

	if b.Mode != nil {
		mode, err := strconv.ParseUint(*b.Mode, 8, 32)
		if err != nil {
			return err
		}

		if e = unix.Fchmod(source, uint32(mode)); e != nil {
			return e
		}
	}

	path := fmt.Sprintf("/proc/self/fd/%d/%s", fd, b.Target)
	if e = unix.Mount(fmt.Sprintf("/proc/self/fd/%d", source), path, "", unix.MS_BIND, ""); e != nil {
		return e
	}

	return unix.Mount("", path, "", unix.MS_PRIVATE, "")
}

func run() error {
	if len(os.Args) != 4 || (os.Args[1] != "pin" && os.Args[1] != "unpin") {
		return errors.New("usage: pinned-bind-sources pin|unpin MANIFEST RUNTIME_DIRECTORY")
	}

	data, e := os.ReadFile(os.Args[2])
	if e != nil {
		return e
	}

	var bindings []binding
	if e = json.Unmarshal(data, &bindings); e != nil {
		return e
	}

	for _, b := range bindings {
		if !numeric.MatchString(b.Target) {
			return errors.New("bind staging names must be numeric")
		}
	}

	fd, e := openDirectory(os.Args[3], false)
	if e != nil {
		if errors.Is(e, unix.ENOENT) && os.Args[1] == "unpin" {
			return nil
		}

		return e
	}
	defer unix.Close(fd)

	var st unix.Stat_t
	if e = unix.Fstat(fd, &st); e != nil {
		return e
	}

	if int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 {
		return errors.New("bind staging directory must be private and owned by the service user")
	}

	if e = unpin(fd); e != nil {
		return e
	}

	if os.Args[1] == "pin" {
		for _, b := range bindings {
			if e = pin(b, fd); e != nil {
				return e
			}
		}

		return nil
	}

	return os.Remove(os.Args[3])
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
