package main

import "strings"

func deviceLimitEnforced(raw string) bool {
	return !strings.EqualFold(strings.TrimSpace(raw), "off")
}
