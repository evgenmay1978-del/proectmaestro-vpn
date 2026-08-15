//go:build linux

package vkturnconf

import (
	"errors"
	"os"
	"syscall"
)

var errConfigFileLocked = errors.New("vkturn: config file is locked")

func acquireConfigFileLock(path string) (func() error, error) {
	return openConfigFileLock(path, false)
}

func tryAcquireConfigFileLock(path string) (func() error, error) {
	return openConfigFileLock(path, true)
}

func openConfigFileLock(path string, nonblocking bool) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	operation := syscall.LOCK_EX
	if nonblocking {
		operation |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), operation); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errConfigFileLocked
		}
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

func syncConfigDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
