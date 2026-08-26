//go:build !linux

package main

import "os"

func readSecureFile(string, secureFileClass, int64) ([]byte, error) {
	return nil, errUnsafeRuntime
}

func verifySecureFile(string, secureFileClass) error {
	return errUnsafeRuntime
}

func verifySecureDirectory(string) error {
	return errUnsafeRuntime
}

func openPinnedSecureFile(string, secureFileClass) (*os.File, error) {
	return nil, errUnsafeRuntime
}

func openPinnedSecureDirectory(string) (*os.File, error) {
	return nil, errUnsafeRuntime
}

func pinnedSecurePath(*os.File) string {
	return ""
}
