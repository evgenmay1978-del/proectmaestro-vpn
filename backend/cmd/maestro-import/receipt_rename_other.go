//go:build !linux

package main

import "os"

func renameReceiptNoReplace(source, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}
