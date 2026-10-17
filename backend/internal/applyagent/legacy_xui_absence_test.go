package applyagent

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

func TestXUIAbsentMarkerDeniesWholePrepareBeforeAnyClientCall(t *testing.T) {
	for _, value := range []any{map[string]any{"schema_version": 1}, nil, false} {
		client := &fakeXUIClient{}
		endpoint, _ := url.Parse("http://127.0.0.1:54321")
		driver, err := NewXUIDriver(XUIDriverConfig{NodeID: "s1", ServiceID: "s1-vless", Endpoint: endpoint, InboundID: 1, Client: client})
		if err != nil {
			t.Fatal(err)
		}
		entry := xuiEntry(t, "customer", "ExactLogin", 1, false)
		var body map[string]any
		if json.Unmarshal(entry.Body, &body) != nil {
			t.Fatal("fixture payload")
		}
		body[controlplane.LegacyXUIAbsenceMarker] = value
		entry.Body, _ = json.Marshal(body)
		snapshot := xuiSnapshot("s1", "s1-vless", xuiEntry(t, "other", "OtherLogin", 1, false), entry)
		if _, err := driver.Inspect(context.Background(), snapshot); err == nil {
			t.Fatal("quarantine reported healthy")
		}
		if _, err := driver.Prepare(context.Background(), snapshot); err == nil {
			t.Fatal("quarantine accepted for upsert")
		}
		if len(client.calls) != 0 {
			t.Fatal("XUI API was called before quarantine rejection")
		}
	}
}
