package api

import (
	"net/url"
	"strings"
)

func normalizeWBStreamRoom(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if olcRoomIDRe.MatchString(input) {
		return input, true
	}
	room, ok := normalizeHTTPSRoomLink(input, "stream.wb.ru", "/room/")
	if !ok || !olcRoomIDRe.MatchString(room) {
		return "", false
	}
	return room, true
}

func normalizeVKCallInput(input string) string {
	input = strings.TrimSpace(input)
	if hash, ok := normalizeHTTPSRoomLink(input, "vk.ru", "/call/join/"); ok {
		return hash
	}
	return input
}

func normalizeHTTPSRoomLink(input, host, prefix string) (string, bool) {
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil ||
		!strings.EqualFold(u.Hostname(), host) || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(path, prefix)
	if value == "" || strings.Contains(value, "/") {
		return "", false
	}
	return value, true
}
