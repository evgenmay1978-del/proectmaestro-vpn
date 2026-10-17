//go:build !linux

package main

import "os"

// This opt-in bridge trusts the Linux ordinary writer's file ownership.
func legacyPrimaryRootOwned(os.FileInfo) bool { return false }
