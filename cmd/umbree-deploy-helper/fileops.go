//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func createTemp(dir, name string, mode os.FileMode) (*os.File, string, error) {
	tmp := filepath.Join(dir, fmt.Sprintf(".%s.%d.tmp", name, os.Getpid()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, "", err
	}
	return f, tmp, nil
}

func commitTemp(f *os.File, tmp, dest string, mode os.FileMode) error {
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(dest))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writeAtomic(dir, name string, data []byte, mode os.FileMode) error {
	f, tmp, err := createTemp(dir, name, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	return commitTemp(f, tmp, filepath.Join(dir, name), mode)
}

func copyAtomic(src, dir, name string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeAtomic(dir, name, data, mode)
}

func sha256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(sum.Sum(nil)), n, nil
}
