//go:build !linux

package release

import "os"

func singleLink(os.FileInfo) bool { return false }
