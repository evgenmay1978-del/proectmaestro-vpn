package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/importer"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/xui"
)

type captureXUIConfig struct {
	SchemaVersion int              `json:"schema_version"`
	Nodes         []captureXUINode `json:"nodes"`
}

type captureXUINode struct {
	NodeID         string     `json:"node_id"`
	ExpectedServer string     `json:"expected_server"`
	Config         xui.Config `json:"config"`
}

type captureXUIClient interface {
	GetClient(string) (*xui.ExistingClient, error)
}

type captureXUIFactory func(xui.Config) (captureXUIClient, error)

func runCaptureXUI(args []string, stdout, stderr io.Writer) int {
	return runCaptureXUIWithFactory(args, stdout, stderr, func(cfg xui.Config) (captureXUIClient, error) { return xui.NewReadOnly(cfg) })
}

func runCaptureXUIWithFactory(args []string, stdout, stderr io.Writer, factory captureXUIFactory) int {
	var customersPath, configPath, outputPath string
	flags := flag.NewFlagSet("maestro-import capture-xui", flag.ContinueOnError)
	// Flag errors can echo unknown argument values; keep all diagnostics fixed.
	flags.SetOutput(io.Discard)
	flags.StringVar(&customersPath, "customers", "", "protected exact legacy customers JSON")
	flags.StringVar(&configPath, "xui-config", "", "protected node/HTTPS Bearer configuration")
	flags.StringVar(&outputPath, "output", "", "exclusive protected capture destination")
	if flags.Parse(args) != nil || flags.NArg() != 0 || customersPath == "" || configPath == "" || outputPath == "" || factory == nil {
		writeError(stderr, "invalid capture arguments")
		return exitInputSystem
	}
	if _, err := os.Lstat(outputPath); !errors.Is(err, os.ErrNotExist) {
		writeError(stderr, "capture output must be new")
		return exitInputSystem
	}
	ctx, cancel := context.WithTimeout(context.Background(), importCommandLimit)
	defer cancel()
	rawConfig, err := readRuntimeFile(configPath, 1<<20, true)
	if err != nil {
		writeError(stderr, "protected capture configuration is unavailable")
		return exitInputSystem
	}
	var cfg captureXUIConfig
	err = strictRuntimeJSON(rawConfig, &cfg)
	zero(rawConfig)
	if err != nil || !validCaptureXUIConfig(cfg) {
		writeError(stderr, "capture configuration is invalid")
		return exitInputSystem
	}
	nodes := make(map[string]captureXUINode, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		nodes[node.NodeID] = node
	}
	// This timestamp precedes the protected source read and every panel lookup.
	capturedAt := time.Now().UTC()
	rawCustomers, err := readRuntimeFile(customersPath, maxSnapshotSize, true)
	if err != nil {
		writeError(stderr, "protected customer source is unavailable")
		return exitInputSystem
	}
	sourceSHA := runtimeSHA256Hex(rawCustomers)
	customers, err := importer.DecodeLegacyCustomers(rawCustomers)
	zero(rawCustomers)
	if err != nil {
		writeError(stderr, "customer source is invalid")
		return exitInputSystem
	}
	capture := importer.LegacyXUICapture{SchemaVersion: 1, CapturedAt: capturedAt, CustomersSHA256: sourceSHA, Bindings: make([]importer.LegacyNodeCapture, 0, len(customers))}
	// Validate every local binding before sending any request. No inferred node,
	// panel UUID, subscription id or source-server replacement is permitted.
	for _, customer := range customers {
		// A retained Hy2/Naive/AnyTLS-only account has no XUI identity to look
		// up. DecodeLegacyCustomers already rejects orphan VLESS3/4 records.
		if customer.VLESS == nil {
			continue
		}
		if customer.VLESS == nil || customer.VLESS.UUID == "" || customer.VLESS.Server == "" ||
			(customer.VLESS3 != nil && customer.VLESS3.UUID != customer.VLESS.UUID) ||
			(customer.VLESS4 != nil && customer.VLESS4.UUID != customer.VLESS.UUID) {
			writeError(stderr, "customer node binding is invalid")
			return exitInputSystem
		}
		bindings := []importer.LegacyNodeCapture{{Login: customer.Login, NodeID: "S1", Server: customer.VLESS.Server, UUID: customer.VLESS.UUID}}
		if customer.VLESS3 != nil {
			bindings = append(bindings, importer.LegacyNodeCapture{Login: customer.Login, NodeID: "S3", Server: customer.VLESS3.Server, UUID: customer.VLESS3.UUID})
		}
		if customer.VLESS4 != nil {
			bindings = append(bindings, importer.LegacyNodeCapture{Login: customer.Login, NodeID: "S4", Server: customer.VLESS4.Server, UUID: customer.VLESS4.UUID})
		}
		for _, binding := range bindings {
			node, exists := nodes[binding.NodeID]
			if !exists || binding.Server == "" || node.ExpectedServer != binding.Server {
				writeError(stderr, "customer server does not match capture node")
				return exitInputSystem
			}
			capture.Bindings = append(capture.Bindings, binding)
		}
	}
	clients := make(map[string]captureXUIClient, len(nodes))
	for i := range capture.Bindings {
		if ctx.Err() != nil {
			writeError(stderr, "capture time limit reached")
			return exitInputSystem
		}
		binding := &capture.Bindings[i]
		client := clients[binding.NodeID]
		if client == nil {
			client, err = factory(nodes[binding.NodeID].Config)
			if err != nil || client == nil {
				writeError(stderr, "capture client is unavailable")
				return exitInputSystem
			}
			clients[binding.NodeID] = client
		}
		record, lookupErr := lookupCaptureXUI(ctx, client, binding.Login)
		if lookupErr != nil || record == nil || record.Email != binding.Login || record.UUID != binding.UUID || record.SubID == "" || len(record.SubID) > 4096 || strings.ContainsRune(record.SubID, 0) {
			writeError(stderr, "capture lookup or identity verification failed")
			return exitInputSystem
		}
		binding.SubID = record.SubID
	}
	finalCustomers, err := readRuntimeFile(customersPath, maxSnapshotSize, true)
	if err != nil {
		writeError(stderr, "customer source recheck failed")
		return exitInputSystem
	}
	finalSHA := runtimeSHA256Hex(finalCustomers)
	zero(finalCustomers)
	if finalSHA != sourceSHA {
		writeError(stderr, "customer source changed during capture")
		return exitInputSystem
	}
	capture.CompletedAt = time.Now().UTC()
	if ctx.Err() != nil || capture.CompletedAt.Before(capturedAt) {
		writeError(stderr, "capture time limit or clock validation failed")
		return exitInputSystem
	}
	output, err := json.Marshal(capture)
	if err != nil || len(output) > maxSnapshotSize {
		zero(output)
		writeError(stderr, "capture output is invalid")
		return exitInputSystem
	}
	defer zero(output)
	if ctx.Err() != nil || writeNormalizeOutput(outputPath, output) != nil {
		writeError(stderr, "capture output cannot be written")
		return exitInputSystem
	}
	_, _ = fmt.Fprintln(stdout, "XUI capture written; no provisioning performed")
	return exitClean
}

func validCaptureXUIConfig(cfg captureXUIConfig) bool {
	if cfg.SchemaVersion != 1 || len(cfg.Nodes) < 1 || len(cfg.Nodes) > 3 {
		return false
	}
	seen := make(map[string]bool, 3)
	for _, node := range cfg.Nodes {
		if (node.NodeID != "S1" && node.NodeID != "S3" && node.NodeID != "S4") || seen[node.NodeID] || node.ExpectedServer == "" || len(node.ExpectedServer) > 4096 || strings.TrimSpace(node.ExpectedServer) != node.ExpectedServer {
			return false
		}
		seen[node.NodeID] = true
		cfg := node.Config
		endpoint, err := url.Parse(cfg.BaseURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || strings.HasSuffix(cfg.BaseURL, "/") || cfg.Insecure || cfg.Token == "" || cfg.Username != "" || cfg.Password != "" {
			return false
		}
	}
	return true
}

// The existing XUI API has no request-context parameter. Its production HTTP
// client bounds each GET to 20 seconds. The buffered result lets the command's
// 30-minute context stop publication/new requests; at most that one already
// running GET may finish under the existing HTTP timeout after cancellation.
func lookupCaptureXUI(ctx context.Context, client captureXUIClient, login string) (*xui.ExistingClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		client *xui.ExistingClient
		err    error
	}
	done := make(chan result, 1)
	go func() { client, err := client.GetClient(login); done <- result{client, err} }()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return response.client, response.err
	}
}
