//go:build !linux

package backuprpo

import "os"

func newSecureBundleRuntime(string) (bundleRuntime, error) {
	return nil, ErrUnsafeRuntime
}

func newPinnedBundleRuntime(*os.File, string) (bundleRuntime, error) {
	return nil, ErrUnsafeRuntime
}
