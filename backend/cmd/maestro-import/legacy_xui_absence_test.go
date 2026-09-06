package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

func TestCaptureXUIOnlySkipsIndependentlyProvenAbsentBinding(t *testing.T) {
	cfg, customers, args, raw := captureFixture(t)
	proof := controlplane.LegacyXUIAbsenceEvidence{SchemaVersion: 1, Method: "xui-sqlite-live-fd-wal-snapshot-v1", ObservedAt: time.Now().UTC(), CustomersSHA256: runtimeSHA256Hex(raw), DatabaseSnapshotSHA256: strings.Repeat("a", 64), DatabaseDevice: 1, DatabaseInode: 2, XUIPID: 3, XUIStartTicks: 4, LiveDatabaseFDMatched: true, InboundsLoginAbsent: true, InboundsUUIDAbsent: true, ClientsLoginAbsent: true, ClientsUUIDAbsent: true}
	cfg.ObservedAbsent = []importer.LegacyNodeCapture{{Login: customers[0].Login, NodeID: "S1", Server: customers[0].VLESS.Server, UUID: customers[0].VLESS.UUID, ObservedAbsent: &proof}}
	writeCaptureJSON(t, args[3], cfg)
	calls := 0
	factory := func(config xui.Config) (captureXUIClient, error) {
		if config.Host == "s1.example.test" {
			t.Fatal("absence proof caused an API lookup")
		}
		return captureXUIClientFunc(func(login string) (*xui.ExistingClient, error) {
			calls++
			return &xui.ExistingClient{Email: login, UUID: customers[0].VLESS.UUID, SubID: "existing-" + config.Host}, nil
		}), nil
	}
	var stdout, stderr bytes.Buffer
	if code := runCaptureXUIWithFactory(args, &stdout, &stderr, factory); code != exitClean || calls != 2 {
		t.Fatal("protected absence did not preserve the other lookups")
	}
	output, err := readRuntimeFile(args[5], maxSnapshotSize, true)
	if err != nil {
		t.Fatal(err)
	}
	var capture importer.LegacyXUICapture
	if strictRuntimeJSON(output, &capture) != nil || len(capture.Bindings) != 3 || capture.Bindings[0].SubID != "" || capture.Bindings[0].ObservedAbsent == nil {
		t.Fatal("capture omitted source binding or invented SubID")
	}
}
