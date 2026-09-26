package api

import (
	"regexp"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// clientRouteIDContract is the Android client's own validation of a native
// runtime profile (app/src/main/java/com/maestrovpn/tv/whitelist/WhiteListRuntime.kt):
// every route_id must be a 64-character lowercase hex digest, otherwise the
// client discards the WHOLE document and its CDN tab stays empty. The flat CDN
// runtime answered positional ids ("flat-cdn-1") from 2026-09-20 to 2026-09-26,
// so every installed client showed "Список серверов пока недоступен" while the
// third-party clients, which read /cdn-sub/ instead, were unaffected.
var clientRouteIDContract = regexp.MustCompile("^[0-9a-f]{64}$")

func flatCDNTestNode(label, path string) subgen.WhiteListNode {
	return subgen.WhiteListNode{
		Protocol: "vless", Network: "xhttp", Address: "188.72.111.7", Port: 443, TLS: true,
		ServerName: "cdn-test.wapmixx.ru", Host: "cdn-test.wapmixx.ru", Path: path,
		Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ClientID:   "beb6297b-a9cf-4903-af8a-c45d60b585cc",
		Encryption: "mlkem768x25519plus.native.0rtt." + strings.Repeat("A", 1579),
		Security:   "tls", ALPN: []string{"h2"}, Fingerprint: "firefox",
		Label: label,
	}
}

func TestFlatCDNNativeProfilesSatisfyTheClientRouteIDContract(t *testing.T) {
	const uuid = "beb6297b-a9cf-4903-af8a-c45d60b585cc"
	spain := flatCDNTestNode("🇪🇸 Испания · CDN", "/static/main/video/segment.ts/2c988fd8/es")
	czech := flatCDNTestNode("🇨🇿 Чехия · CDN", "/static/main/video/segment.ts/2c988fd8/cz")

	profiles, err := flatCDNNativeProfiles([]subgen.WhiteListNode{spain, czech}, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(profiles))
	}
	seen := make(map[string]bool, len(profiles))
	for index, profile := range profiles {
		if !clientRouteIDContract.MatchString(profile.RouteID) {
			t.Fatalf("profile %d route_id %q is not the 64-hex digest the client requires", index, profile.RouteID)
		}
		if seen[profile.RouteID] {
			t.Fatalf("profile %d reuses route_id %q", index, profile.RouteID)
		}
		seen[profile.RouteID] = true
		if profile.Label == "" || profile.Label != strings.TrimSpace(profile.Label) ||
			profile.ClientID != uuid || profile.ServerName != profile.Host || profile.Port != 443 {
			t.Fatalf("profile %d violates the client document rules: %+v", index, profile)
		}
		if profile.Path != []subgen.WhiteListNode{spain, czech}[index].Path {
			t.Fatalf("profile %d lost the node transport identity", index)
		}
	}

	again, err := flatCDNNativeProfiles([]subgen.WhiteListNode{spain, czech}, uuid)
	if err != nil || again[0].RouteID != profiles[0].RouteID || again[1].RouteID != profiles[1].RouteID {
		t.Fatal("an unchanged node must keep its route identity across renewals")
	}

	// The client matches a route across renewals by tag, so a purely cosmetic
	// relabel must not look like a brand-new route.
	relabelled, err := flatCDNNativeProfiles([]subgen.WhiteListNode{flatCDNTestNode("CDN 1", spain.Path)}, uuid)
	if err != nil || relabelled[0].RouteID != profiles[0].RouteID {
		t.Fatal("a cosmetic relabel changed the route identity")
	}

	// A different transport path or credential is a different route.
	for name, node := range map[string]subgen.WhiteListNode{
		"path":     flatCDNTestNode(spain.Label, "/static/main/video/segment.ts/00000000/es"),
		"uuid":     flatCDNTestNode(spain.Label, spain.Path),
	} {
		candidate := node
		if name == "uuid" {
			candidate.ClientID = "11111111-1111-4111-8111-111111111111"
		}
		changed, err := flatCDNNativeProfiles([]subgen.WhiteListNode{candidate}, uuid)
		if err != nil || changed[0].RouteID == profiles[0].RouteID {
			t.Fatalf("%s change reused the previous route identity", name)
		}
	}

	// A node without a label still gets a client-valid label, not an empty one.
	unlabelled, err := flatCDNNativeProfiles([]subgen.WhiteListNode{flatCDNTestNode("", spain.Path)}, uuid)
	if err != nil || unlabelled[0].Label != "CDN 1" {
		t.Fatalf("unlabelled node = %q, want %q", unlabelled[0].Label, "CDN 1")
	}
}
