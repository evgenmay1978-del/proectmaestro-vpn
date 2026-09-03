package release_test

import (
	"bytes"
	"encoding/json"
	"testing"

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
