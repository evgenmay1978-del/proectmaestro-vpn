//go:build windows

package release

import "os"

// Windows CI does not expose a portable link-count field through os.FileInfo.
// The sealed-directory and open-handle identity checks still apply there.
func singleLink(info os.FileInfo) bool { return info != nil }
