package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHandleReportStoresUpdateOutcome pins the contract the app's UpdateTelemetry relies on:
// a kind:"update" event (download/install outcome) is ingested by the SAME /report sink as
// crash/hello and lands verbatim in reports-<day>.jsonl.
//
// WHY this is a test and not a one-off curl: the whole point of the update events is to make
// «постоянно загрузка» falsifiable from S1 (the APK bytes come from the Yandex mirror, so the
// server sees nothing otherwise — see memory/tv-update-stuck-loading-2026-07-30). If a future
// tightening of handleReport silently drops unknown kinds, that visibility dies quietly and we
// are back to guessing. A probe POST against prod would also pollute the append-only log forever.
func TestHandleReportStoresUpdateOutcome(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: Config{ReportDir: dir}}

	body := `{"kind":"update","v":"1.0.153","vc":153,"device":"TCL Smart TV Pro","api":31,` +
		`"id":"d-test","ts":1785410743167,"msg":"download_failed",` +
		`"stack":"target=153 have=41943040 want=116379909 attempts=8 free=180MB err=size mismatch"}`
	req := httptest.NewRequest(http.MethodPost, "/report", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleReport(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}

	path := filepath.Join(dir, "reports-"+time.Now().UTC().Format("2006-01-02")+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("report file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines: got %d, want 1 (one physical line per report)", len(lines))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("stored line is not JSON: %v", err)
	}
	for field, want := range map[string]any{
		"kind":   "update",
		"msg":    "download_failed",
		"device": "TCL Smart TV Pro",
		"vc":     float64(153),
		"api":    float64(31),
	} {
		if got[field] != want {
			t.Errorf("%s: got %v, want %v", field, got[field], want)
		}
	}
	// The detail line is what triage actually reads — it must survive intact, not be clipped
	// away or stripped (it is plain key=value, well under the 8000-byte cap).
	if s, _ := got["stack"].(string); !strings.Contains(s, "want=116379909") || !strings.Contains(s, "free=180MB") {
		t.Errorf("stack lost detail: %q", got["stack"])
	}
}
