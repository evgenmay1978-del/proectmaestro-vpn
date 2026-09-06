//go:build linux

package main

import (
	"os"
	"syscall"
)

func legacyPrimaryRootOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && (!info.Mode().IsRegular() || stat.Nlink == 1)
}
