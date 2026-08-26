//go:build !linux

package backuprpo

import "os"

func newSecureManifestVerificationRuntime(string) (manifestVerificationRuntime, error) {
	return nil, ErrUnsafeRuntime
}

func newPinnedManifestVerificationRuntime(*os.File, string) (manifestVerificationRuntime, error) {
	return nil, ErrUnsafeRuntime
}
