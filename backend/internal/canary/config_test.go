package canary_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/canary"
)

func decodeConfig(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return value
}

func mapValue(t *testing.T, value any) map[string]any {
	t.Helper()
	got, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return got
}

func arrayValue(t *testing.T, value any) []any {
	t.Helper()
	got, ok := value.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", value)
	}
	return got
}

func TestMaterializeBuildsIsolatedXHTTPPair(t *testing.T) {
	snapshot, err := canary.NewSnapshot(testRequest(), testMaterial())
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := snapshot.Materialize()
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	server := decodeConfig(t, artifacts.ServerConfig())
	inbounds := arrayValue(t, server["inbounds"])
	if len(inbounds) != 2 {
		t.Fatalf("server inbound count = %d", len(inbounds))
	}
	public := mapValue(t, inbounds[0])
	if public["protocol"] != "vless" || public["listen"] != "0.0.0.0" || public["port"] != float64(18081) {
		t.Fatalf("unexpected public inbound: %#v", public)
	}
	stream := mapValue(t, public["streamSettings"])
	xhttp := mapValue(t, stream["xhttpSettings"])
	if stream["network"] != "xhttp" || xhttp["mode"] != "packet-up" || xhttp["uplinkHTTPMethod"] != "GET" || xhttp["uplinkDataPlacement"] != "body" {
		t.Fatalf("unexpected xhttp transport: %#v", stream)
	}
	clients := arrayValue(t, mapValue(t, public["settings"])["clients"])
	if len(clients) != 1 {
		t.Fatalf("server client count = %d", len(clients))
	}
	client := mapValue(t, clients[0])
	if len(client) != 3 || client["id"] != testMaterial().ClientID || client["email"] != testMaterial().ClientEmail || client["level"] != float64(0) {
		t.Fatalf("server client leaked fields or mismatched: %#v", client)
	}
	if mapValue(t, public["settings"])["decryption"] != testMaterial().ServerDecryption {
		t.Fatal("server decryption mismatched")
	}
	if bytes.Contains(artifacts.ServerConfig(), []byte(`"encryption"`)) {
		t.Fatal("server config contains client encryption")
	}

	direct := decodeConfig(t, artifacts.DirectClientConfig())
	cdn := decodeConfig(t, artifacts.CDNClientConfig())
	for _, config := range []map[string]any{direct, cdn} {
		log := mapValue(t, config["log"])
		if log["access"] != "none" || log["error"] != "none" || log["loglevel"] != "warning" {
			t.Fatal("client log boundary mismatched")
		}
	}
	assertClientPair(t, artifacts.ClientURI(), direct, cdn)

	api := mapValue(t, inbounds[1])
	if api["listen"] != "127.0.0.1" || api["port"] != float64(18082) {
		t.Fatalf("stats API is not loopback-only: %#v", api)
	}
	policy := mapValue(t, server["policy"])
	levels := mapValue(t, policy["levels"])
	levelZero := mapValue(t, levels["0"])
	if levelZero["statsUserUplink"] != true || levelZero["statsUserDownlink"] != true {
		t.Fatalf("missing per-user stats: %#v", levelZero)
	}

	receipt := artifacts.Receipt()
	if bytes.Contains(receipt, []byte(testMaterial().SecretPath)) || bytes.Contains(receipt, []byte(testRequest().DiagnosticProbeURL)) || bytes.Contains(receipt, []byte(testMaterial().ClientID)) || !bytes.Contains(receipt, []byte("baseline_default_padding")) || bytes.Contains(receipt, []byte("baseline_unpadded")) || !bytes.Contains(receipt, []byte("maestro_advanced_not_claimed")) {
		t.Fatalf("receipt leaked operational data or claimed advanced completion: %s", receipt)
	}
	var receiptFields map[string]any
	if err := json.Unmarshal(receipt, &receiptFields); err != nil || len(receiptFields) != 7 {
		t.Fatal("receipt field allowlist mismatch")
	}

	first := artifacts.ServerConfig()
	first[0] = '!'
	if bytes.Equal(first, artifacts.ServerConfig()) {
		t.Fatal("artifacts exposed mutable config bytes")
	}
}

func assertClientPair(t *testing.T, rawURI []byte, direct, cdn map[string]any) {
	t.Helper()
	directOutbound := mapValue(t, arrayValue(t, direct["outbounds"])[0])
	cdnOutbound := mapValue(t, arrayValue(t, cdn["outbounds"])[0])
	directVNext := mapValue(t, arrayValue(t, mapValue(t, directOutbound["settings"])["vnext"])[0])
	cdnVNext := mapValue(t, arrayValue(t, mapValue(t, cdnOutbound["settings"])["vnext"])[0])
	if directVNext["address"] != "127.0.0.1" || directVNext["port"] != float64(18081) || cdnVNext["address"] != testRequest().PublicHost || cdnVNext["port"] != float64(443) {
		t.Fatalf("unexpected client targets: direct=%#v cdn=%#v", directVNext, cdnVNext)
	}
	directStream := mapValue(t, directOutbound["streamSettings"])
	cdnStream := mapValue(t, cdnOutbound["streamSettings"])
	if directStream["security"] != "none" || cdnStream["security"] != "tls" {
		t.Fatalf("unexpected TLS modes: direct=%#v cdn=%#v", directStream, cdnStream)
	}
	if mapValue(t, cdnStream["tlsSettings"])["serverName"] != testRequest().PublicHost || mapValue(t, cdnStream["xhttpSettings"])["host"] != testRequest().PublicHost {
		t.Fatalf("CDN SNI/Host do not equal public host: %#v", cdnStream)
	}
	for _, config := range []map[string]any{direct, cdn} {
		inbound := mapValue(t, arrayValue(t, config["inbounds"])[0])
		if inbound["protocol"] != "socks" || inbound["listen"] != "127.0.0.1" {
			t.Fatalf("test SOCKS inbound is not loopback-only: %#v", inbound)
		}
		outbound := mapValue(t, arrayValue(t, config["outbounds"])[0])
		user := mapValue(t, arrayValue(t, mapValue(t, arrayValue(t, mapValue(t, outbound["settings"])["vnext"])[0])["users"])[0])
		if user["id"] != testMaterial().ClientID || user["encryption"] != testMaterial().ClientEncryption {
			t.Fatalf("client credentials are wrong: %#v", user)
		}
		xhttp := mapValue(t, mapValue(t, outbound["streamSettings"])["xhttpSettings"])
		if xhttp["path"] != testMaterial().SecretPath || xhttp["sessionIDPlacement"] != "query" || xhttp["seqPlacement"] != "query" {
			t.Fatalf("pair metadata mismatched: %#v", xhttp)
		}
	}
	directPort := mapValue(t, arrayValue(t, direct["inbounds"])[0])["port"]
	cdnPort := mapValue(t, arrayValue(t, cdn["inbounds"])[0])["port"]
	if directPort != float64(10808) || cdnPort != float64(10809) {
		t.Fatal("SOCKS test ports mismatched")
	}
	parsed, err := url.Parse(string(rawURI))
	if err != nil || parsed.Scheme != "vless" || parsed.User.Username() != testMaterial().ClientID || parsed.Host != testRequest().PublicHost+":443" || strings.Contains(string(rawURI), testMaterial().ServerDecryption) {
		t.Fatal("invalid client URI")
	}
	query := parsed.Query()
	if len(query) != 8 || query.Get("encryption") != testMaterial().ClientEncryption || query.Get("security") != "tls" || query.Get("sni") != testRequest().PublicHost || query.Get("host") != testRequest().PublicHost || query.Get("path") != testMaterial().SecretPath || query.Get("mode") != "packet-up" || query.Get("type") != "xhttp" {
		t.Fatal("URI contract mismatch")
	}
	if query.Has("uplinkHTTPMethod") || query.Has("uplinkDataPlacement") {
		t.Fatal("low-frequency XHTTP fields must be carried only inside extra")
	}
	var extra map[string]any
	if err := json.Unmarshal([]byte(query.Get("extra")), &extra); err != nil || len(extra) != 7 || extra["sessionIDPlacement"] != "query" || extra["sessionIDKey"] != "auth" || extra["sessionIDLength"] != float64(16) || extra["seqPlacement"] != "query" || extra["seqKey"] != "chunk_id" || extra["uplinkHTTPMethod"] != "GET" || extra["uplinkDataPlacement"] != "body" {
		t.Fatalf("URI extra mismatch: %q", query.Get("extra"))
	}
}
