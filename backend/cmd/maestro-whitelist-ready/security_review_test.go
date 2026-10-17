package main

import "testing"

func TestSafeLocalPathRejectsNetworkDeviceAndTraversalForms(t *testing.T) {
	tests := []string{
		`\\server\share\catalog.json`,
		`\\?\UNC\server\share\catalog.json`,
		`\\.\PIPE\fixture`,
		`C:\absolute\catalog.json`,
		`C:drive-relative.json`,
		`..\outside.json`,
		`../outside.json`,
		`/absolute/catalog.json`,
		`\rooted\catalog.json`,
		`//server/share/catalog.json`,
		`c:drive-relative.json`,
		`dir/../../outside.json`,
		`dir\..\..\outside.json`,
		`dir/../catalog.json`,
		`dir\..\catalog.json`,
		`file:\\server\share\catalog.json`,
	}
	for _, value := range tests {
		if safeLocalPath(value) {
			t.Fatalf("unsafe path accepted: %q", value)
		}
	}
	for _, value := range []string{"catalog.json", "scripts/repro/fixtures/catalog.json", `scripts\repro\fixtures\catalog.json`} {
		if !safeLocalPath(value) {
			t.Fatalf("repo-local path rejected: %q", value)
		}
	}
}
