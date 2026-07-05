package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// crashReport is the compact diagnostic an app instance POSTs after a crash/OOM it
// recorded locally on a PREVIOUS run (a crash can't reliably phone home while dying, so
// the app uploads pending local reports on its next launch). Deliberately minimal and
// PII-free: app version, device model, Android level, an anonymous per-install id, and
// the failure itself — NO traffic, NO credentials, NO sub token, NO login.
type crashReport struct {
	Kind      string `json:"kind"`   // "crash" | "oom" | "error"
	Version   string `json:"v"`      // app versionName, e.g. 1.0.103
	Code      int    `json:"vc"`     // app versionCode
	Device    string `json:"device"` // brand + model
	API       int    `json:"api"`    // Android SDK int
	InstallID string `json:"id"`     // anonymous per-install UUID
	When      int64  `json:"ts"`     // device epoch millis when it happened (optional)
	Message   string `json:"msg"`    // first line / exception summary
	Stack     string `json:"stack"`  // truncated stack trace / OOM metadata
}

// reportMu serializes appends so concurrent device uploads can't interleave a line.
var reportMu sync.Mutex

// handleReport ingests an app crash/diagnostic report into the S1 reports log so the
// fleet's real failures are visible without waiting for a customer to complain. It is
// PUBLIC (devices hold no admin token) but defensive by construction: POST only, body
// bounded to 64KB by decodeJSON, every field sanitized + length-capped, and stored
// append-only as one JSON object per line in a per-day file. Best-effort: a storage
// error still returns 204 so a device never loops retrying a report it can't deliver.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var rep crashReport
	if !decodeJSON(w, r, &rep) {
		return // decodeJSON already wrote the 400
	}

	// Re-marshal a sanitized record with a SERVER timestamp (device clocks lie). Short
	// fields are stripped to printable single-line text; the multi-line stack is only
	// length-clipped — json.Marshal escapes its newlines, so the JSON-lines file stays
	// one physical line per report regardless.
	rec := map[string]any{
		"at":     time.Now().UTC().Format(time.RFC3339),
		"kind":   clipStr(sanitizeLine(rep.Kind), 24),
		"v":      clipStr(sanitizeLine(rep.Version), 24),
		"vc":     rep.Code,
		"device": clipStr(sanitizeLine(rep.Device), 80),
		"api":    rep.API,
		"id":     clipStr(sanitizeLine(rep.InstallID), 64),
		"ts":     rep.When,
		"msg":    clipStr(sanitizeLine(rep.Message), 400),
		"stack":  clipStr(rep.Stack, 8000),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	reportMu.Lock()
	if mkErr := os.MkdirAll(s.cfg.ReportDir, 0o750); mkErr == nil {
		day := time.Now().UTC().Format("2006-01-02")
		path := filepath.Join(s.cfg.ReportDir, "reports-"+day+".jsonl")
		if f, oErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); oErr == nil {
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}
	reportMu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

// sanitizeLine collapses whitespace controls to a space and drops other control bytes,
// keeping a field to printable single-line text (defends the log against junk/injection).
func sanitizeLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20:
			return -1
		default:
			return r
		}
	}, s)
}

// clipStr caps a string to n bytes (json.Marshal tolerates a rune cut mid-way).
func clipStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
