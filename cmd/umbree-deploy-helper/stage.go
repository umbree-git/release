//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
)

const stagedName = binaryName + ".new"

func checkStageDir(st *syscall.Stat_t, deployUID int) error {
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return fmt.Errorf("STAGE_DIR is not a directory")
	}
	if int(st.Uid) != deployUID {
		return fmt.Errorf("STAGE_DIR is owned by uid %d, not the deploy user (uid %d)", st.Uid, deployUID)
	}
	if st.Mode&0o027 != 0 {
		return fmt.Errorf("STAGE_DIR mode %04o is wider than 0750", st.Mode&0o7777)
	}
	return nil
}

func checkSource(st *syscall.Stat_t, deployUID int) error {
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		return fmt.Errorf("%s is not a regular file", stagedName)
	}
	if int(st.Uid) != deployUID {
		return fmt.Errorf("%s is owned by uid %d, not the deploy user (uid %d)", stagedName, st.Uid, deployUID)
	}
	if st.Nlink != 1 {
		return fmt.Errorf("%s has %d links, want 1", stagedName, st.Nlink)
	}
	return nil
}

func openStageDir(dir string, deployUID int) (int, error) {
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open STAGE_DIR %s (a symlink is refused): %w", dir, err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if err := checkStageDir(&st, deployUID); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func openStagedBinary(dir string, deployUID int) (*os.File, error) {
	dfd, err := openStageDir(dir, deployUID)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(dfd)
	fd, err := syscall.Openat(dfd, stagedName, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s (a symlink is refused): %w", stagedName, err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if err := checkSource(&st, deployUID); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), stagedName), nil
}
