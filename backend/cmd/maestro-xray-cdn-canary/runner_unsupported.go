//go:build !linux

package main

func newPlatformOperator() (commandOperator, error) {
	return nil, reasonError{"unsupported_platform"}
}
