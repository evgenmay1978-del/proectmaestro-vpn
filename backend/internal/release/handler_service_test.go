package release_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/release"
)

func TestTask12TemplatePinsHandlerServiceRelayMatrixAndFailClosedRouting(t *testing.T) {
	raw := release.DefaultConfigTemplate()
	if err := release.ValidateConfigTemplate(raw); err != nil {
		t.Fatalf("ValidateConfigTemplate: %v", err)
	}
	if bytes.Contains(bytes.ToLower(raw), []byte("private key")) || bytes.Contains(raw, []byte("vless://")) {
		t.Fatal("immutable template contains relay secret material")
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	api := config["api"].(map[string]any)
	services := api["services"].([]any)
	if !equalJSONStrings(services, []string{"StatsService", "HandlerService"}) {
		t.Fatalf("api services = %#v", services)
	}
	inbounds := config["inbounds"].([]any)
	if len(inbounds) != 3 || inboundByTag(inbounds, "api")["listen"] != "127.0.0.1" || inboundByTag(inbounds, "api")["port"] != float64(18082) || inboundByTag(inbounds, "maestro-cdn-exit-in")["port"] != float64(18084) {
		t.Fatalf("isolated inbound boundary = %#v", inbounds)
	}
	outbounds := config["outbounds"].([]any)
	for _, tag := range []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4", "direct", "block"} {
		if outboundByTag(outbounds, tag) == nil {
			t.Fatalf("missing outbound %q", tag)
		}
	}
	rules := config["routing"].(map[string]any)["rules"].([]any)
	for _, exit := range []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"} {
		if !hasUserRoute(rules, "regexp:^wl:[^:]+:"+exit+"$", exit) {
			t.Fatalf("missing exact managed route for %s", exit)
		}
	}
	if !hasInboundRoute(rules, "maestro-cdn-exit-in", "direct") || !hasInboundRoute(rules, "maestro-cdn-in", "block") {
		t.Fatalf("relay escape or terminal blackhole missing: %#v", rules)
	}
}

func TestTask12TemplateRejectsIncompleteOrLoopingRouteMatrix(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal(release.DefaultConfigTemplate(), &config); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	outbounds := config["outbounds"].([]any)
	config["outbounds"] = outbounds[:len(outbounds)-1]
	incomplete, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal incomplete: %v", err)
	}
	if err := release.ValidateConfigTemplate(incomplete); err == nil {
		t.Fatal("incomplete Origin-to-exit matrix accepted")
	}

	if err := json.Unmarshal(release.DefaultConfigTemplate(), &config); err != nil {
		t.Fatalf("json.Unmarshal loop: %v", err)
	}
	rules := config["routing"].(map[string]any)["rules"].([]any)
	for _, rawRule := range rules {
		rule := rawRule.(map[string]any)
		if rule["outboundTag"] == "direct" {
			rule["outboundTag"] = "exit-s1"
			break
		}
	}
	looping, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal looping: %v", err)
	}
	if err := release.ValidateConfigTemplate(looping); err == nil {
		t.Fatal("relay loop accepted")
	}
}

func TestTask12RuntimeRelayMaterialIsDigestBoundAndHasExactlyOneLocalExit(t *testing.T) {
	material := release.RuntimeMaterial{
		ServerDecryption: "synthetic-runtime-server-decryption",
		LocalExitID:      "exit-s1",
		RelayRoutes: []release.RelayRouteMaterial{
			{ExitID: "exit-s1", Address: "127.0.0.1", ServerName: "exit-s1.example.test", Credential: "00000000-0000-4000-8000-000000000011"},
			{ExitID: "exit-s2", Address: "192.0.2.12", ServerName: "exit-s2.example.test", Credential: "00000000-0000-4000-8000-000000000012"},
			{ExitID: "exit-s3", Address: "192.0.2.13", ServerName: "exit-s3.example.test", Credential: "00000000-0000-4000-8000-000000000013"},
			{ExitID: "exit-s4", Address: "192.0.2.14", ServerName: "exit-s4.example.test", Credential: "00000000-0000-4000-8000-000000000014"},
		},
	}
	runtimeDigest, err := release.RuntimeMaterialSHA256(material)
	if err != nil {
		t.Fatalf("RuntimeMaterialSHA256: %v", err)
	}
	spec, _, privateKey := taskASpec(t, "release-relay")
	spec.RuntimeMaterialSHA256 = runtimeDigest
	evidence, err := release.BuildValidationEvidence(spec, taskASignedReports(t, spec, privateKey, time.Now().UTC()))
	if err != nil {
		t.Fatalf("BuildValidationEvidence: %v", err)
	}
	spec.ValidationEvidence = evidence
	candidate, err := release.NewCandidate(spec)
	if err != nil {
		t.Fatalf("NewCandidate: %v", err)
	}
	runtimeConfig, err := candidate.MaterializeRuntimeConfig(material)
	if err != nil {
		t.Fatalf("MaterializeRuntimeConfig: %v", err)
	}
	for _, expected := range []string{"127.0.0.1", "exit-s4.example.test", material.RelayRoutes[2].Credential, `"allowInsecure":false`, `"alpn":["h2"]`} {
		if !bytes.Contains(runtimeConfig, []byte(expected)) {
			t.Fatalf("runtime relay config missing %q", expected)
		}
	}
	if bytes.Contains(release.DefaultConfigTemplate(), []byte(material.RelayRoutes[2].Credential)) ||
		bytes.Contains(bytes.ToLower(runtimeConfig), []byte("<runtime_")) {
		t.Fatal("relay secret leaked into immutable template or runtime placeholder remained")
	}
	if strings.Count(string(runtimeConfig), `"email":"relay:`) != 1 {
		t.Fatal("relay inbound accepts a credential for a non-local exit")
	}

	for name, mutate := range map[string]func(*release.RuntimeMaterial){
		"missing":            func(value *release.RuntimeMaterial) { value.LocalExitID, value.RelayRoutes = "", nil },
		"incomplete":         func(value *release.RuntimeMaterial) { value.RelayRoutes = value.RelayRoutes[:3] },
		"no local relay":     func(value *release.RuntimeMaterial) { value.RelayRoutes[0].Address = "192.0.2.11" },
		"wrong local exit":   func(value *release.RuntimeMaterial) { value.LocalExitID = "exit-s2" },
		"invalid SNI":        func(value *release.RuntimeMaterial) { value.RelayRoutes[1].ServerName = "exit-s2.invalid" },
		"invalid credential": func(value *release.RuntimeMaterial) { value.RelayRoutes[1].Credential = strings.Repeat("0", 36) },
	} {
		t.Run(name, func(t *testing.T) {
			copyMaterial := material
			copyMaterial.RelayRoutes = append([]release.RelayRouteMaterial(nil), material.RelayRoutes...)
			mutate(&copyMaterial)
			if _, err := release.RuntimeMaterialSHA256(copyMaterial); err == nil {
				t.Fatal("invalid relay runtime material accepted")
			}
		})
	}
}

func equalJSONStrings(actual []any, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return false
		}
	}
	return true
}

func inboundByTag(values []any, tag string) map[string]any {
	for _, value := range values {
		inbound := value.(map[string]any)
		if inbound["tag"] == tag {
			return inbound
		}
	}
	return nil
}

func outboundByTag(values []any, tag string) map[string]any {
	for _, value := range values {
		outbound := value.(map[string]any)
		if outbound["tag"] == tag {
			return outbound
		}
	}
	return nil
}

func hasUserRoute(values []any, user, outbound string) bool {
	for _, value := range values {
		rule := value.(map[string]any)
		users, _ := rule["user"].([]any)
		if len(users) == 1 && users[0] == user && rule["outboundTag"] == outbound {
			return true
		}
	}
	return false
}

func hasInboundRoute(values []any, inbound, outbound string) bool {
	for _, value := range values {
		rule := value.(map[string]any)
		inbounds, _ := rule["inboundTag"].([]any)
		if len(inbounds) == 1 && inbounds[0] == inbound && rule["outboundTag"] == outbound {
			return true
		}
	}
	return false
}
