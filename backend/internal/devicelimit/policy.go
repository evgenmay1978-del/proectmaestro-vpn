// Package devicelimit owns the production per-login device admission policy.
package devicelimit

import "strings"

const (
	// Disabled bypasses admission without recording a device.
	Disabled = -1
	// Unlimited admits and records every distinct device.
	Unlimited = 0
	// Default is the ordinary per-login device cap.
	Default = 5
)

// ForLogin returns the case-insensitive production limit for login.
func ForLogin(login string) int {
	switch strings.ToLower(login) {
	case "wapmix", "wapmixx", "wapmix2":
		return Unlimited
	case "strogino":
		return 9
	default:
		return Default
	}
}
