package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const haPanelArtifactSHA = "f577c67ad229fe89278430d35a3ec65f6ce454e5"
const haPanelArtifactMainSHA256 = "5f7a044b73f32a87ec04390a0149f37f04609ea46589b00c377c8d13fc2c8908"

func TestHAServiceTemplateRuntimeContract(t *testing.T) {
	repositoryRoot := haTemplateRepositoryRoot(t)
	environment := haTemplateEnvironment(t, filepath.Join(repositoryRoot, "deploy", "ha", "maestro-panel.env.example"))

	wantKeys := []string{
		"MAESTRO_CONTROL_PLANE",
		"MAESTRO_LISTEN",
		"MAESTRO_REPORT_DIR",
		"MAESTRO_RQLITE_CA_FILE",
		"MAESTRO_RQLITE_CERT_FILE",
		"MAESTRO_RQLITE_ENDPOINTS",
		"MAESTRO_RQLITE_KEY_BUNDLE_FILE",
		"MAESTRO_RQLITE_KEY_FILE",
	}
	gotKeys := make([]string, 0, len(environment))
	for key := range environment {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	if strings.Join(gotKeys, "\n") != strings.Join(wantKeys, "\n") {
		t.Fatalf("panel environment keys = %v, want %v", gotKeys, wantKeys)
	}

	for key, value := range environment {
		environment[key] = strings.ReplaceAll(value, "__NODE_ID__", "s2")
		t.Setenv(key, environment[key])
	}
	if reportDir := rqliteAPIConfigFromEnvironment().ReportDir; reportDir != "/var/lib/maestro/panel/reports" {
		t.Fatalf("runtime report directory = %q", reportDir)
	}
	haAssertProductionComposition(t, repositoryRoot)

	legacyCalls := 0
	rqliteCalls := 0
	wantRuntime := &panelRuntime{mode: "rqlite"}
	gotRuntime, err := buildConfiguredRuntime(context.Background(), haTemplateGetenv(environment), configuredRuntimeFactories{
		legacy: func(context.Context) (*panelRuntime, error) {
			legacyCalls++
			return &panelRuntime{mode: "legacy"}, nil
		},
		rqlite: func(_ context.Context, config rqliteRuntimeConfig) (*panelRuntime, error) {
			rqliteCalls++
			wantEndpoints := []string{
				"https://s2-rqlite-http.internal:4001",
				"https://s3-rqlite-http.internal:4001",
				"https://s4-rqlite-http.internal:4001",
			}
			if strings.Join(config.Endpoints, "\n") != strings.Join(wantEndpoints, "\n") {
				t.Fatalf("rqlite endpoints = %v, want %v", config.Endpoints, wantEndpoints)
			}
			if config.CAFile != "/etc/maestro/ha/pki/rqlite-http/ca.pem" {
				t.Fatalf("CA file = %q", config.CAFile)
			}
			if config.CertFile != "/etc/maestro/ha/pki/rqlite-http/s2-panel-rqlite-client.pem" {
				t.Fatalf("certificate file = %q", config.CertFile)
			}
			if config.KeyFile != "/etc/maestro/ha/secrets/rqlite-http/s2-panel-rqlite-client.key" {
				t.Fatalf("key file = %q", config.KeyFile)
			}
			if config.KeyBundleFile != "/etc/maestro/ha/secrets/control-plane/key-bundle.json" {
				t.Fatalf("key bundle file = %q", config.KeyBundleFile)
			}
			return wantRuntime, nil
		},
	})
	if err != nil {
		t.Fatalf("buildConfiguredRuntime: %v", err)
	}
	if gotRuntime != wantRuntime || legacyCalls != 0 || rqliteCalls != 1 {
		t.Fatalf("runtime=%p legacy=%d rqlite=%d, want=%p/0/1", gotRuntime, legacyCalls, rqliteCalls, wantRuntime)
	}

	host, port, err := net.SplitHostPort(environment["MAESTRO_LISTEN"])
	if err != nil {
		t.Fatalf("MAESTRO_LISTEN: %v", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || port != "8910" {
		t.Fatalf("MAESTRO_LISTEN = %q, want loopback port 8910", environment["MAESTRO_LISTEN"])
	}

	servicePath := filepath.Join(repositoryRoot, "deploy", "ha", "maestro-panel.service")
	serviceBytes, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("read panel service template: %v", err)
	}
	service := string(serviceBytes)
	wantExec := "ExecStart=/opt/maestro/ha/releases/" + haPanelArtifactSHA + "/maestro-panel"
	if strings.Count(service, wantExec) != 1 {
		t.Fatalf("panel service does not launch the exact immutable artifact path")
	}
	if strings.Contains(service, "[Install]") || strings.Contains(service, "ExecStart=/bin/") {
		t.Fatalf("panel service is not inert or invokes a shell")
	}
	if strings.Contains(strings.ToLower(service+"\n"+haTemplateSerializedEnvironment(environment)), "olcrtc") ||
		strings.Contains(strings.ToLower(service+"\n"+haTemplateSerializedEnvironment(environment)), "wdtt") {
		t.Fatalf("frozen protocol leaked into HA panel template")
	}
}

func haAssertProductionComposition(t *testing.T, repositoryRoot string) {
	t.Helper()
	path := filepath.Join(repositoryRoot, "backend", "cmd", "maestro-panel", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production main.go: %v", err)
	}
	normalizedSource := bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	if bytes.Contains(normalizedSource, []byte("\r")) {
		t.Fatal("production main.go contains a non-canonical carriage return")
	}
	digest := sha256.Sum256(normalizedSource)
	if got := hex.EncodeToString(digest[:]); got != haPanelArtifactMainSHA256 {
		t.Fatalf("production main.go digest = %q, want pinned artifact source %q", got, haPanelArtifactMainSHA256)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, normalizedSource, 0)
	if err != nil {
		t.Fatalf("parse production main.go: %v", err)
	}
	var mainBody *ast.BlockStmt
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "main" && function.Recv == nil {
			mainBody = function.Body
			break
		}
	}
	if mainBody == nil {
		t.Fatal("production main function is missing")
	}
	runtimeConfigCalls := 0
	rqliteRuntimeCalls := 0
	controlPlaneLiteralCount := 0
	listenBinding := false
	serverUsesListen := false
	ast.Inspect(mainBody, func(node ast.Node) bool {
		switch current := node.(type) {
		case *ast.CallExpr:
			if identifier, ok := current.Fun.(*ast.Ident); ok {
				switch identifier.Name {
				case "readRQLiteRuntimeConfig":
					runtimeConfigCalls++
				case "buildRQLitePanelRuntime":
					rqliteRuntimeCalls++
				}
			}
		case *ast.BasicLit:
			if current.Kind == token.STRING && current.Value == `"MAESTRO_CONTROL_PLANE"` {
				controlPlaneLiteralCount++
			}
		case *ast.AssignStmt:
			if len(current.Lhs) == 1 && len(current.Rhs) == 1 {
				left, leftOK := current.Lhs[0].(*ast.Ident)
				call, callOK := current.Rhs[0].(*ast.CallExpr)
				if !leftOK || !callOK {
					break
				}
				function, functionOK := call.Fun.(*ast.Ident)
				if functionOK && left.Name == "listen" && function.Name == "env" && len(call.Args) == 2 {
					key, keyOK := call.Args[0].(*ast.BasicLit)
					fallback, fallbackOK := call.Args[1].(*ast.BasicLit)
					listenBinding = keyOK && fallbackOK && key.Value == `"MAESTRO_LISTEN"` &&
						fallback.Value == `"127.0.0.1:8910"`
				}
			}
		case *ast.CompositeLit:
			selector, ok := current.Type.(*ast.SelectorExpr)
			if !ok {
				break
			}
			packageName, packageOK := selector.X.(*ast.Ident)
			if !packageOK || packageName.Name != "http" || selector.Sel.Name != "Server" {
				break
			}
			for _, element := range current.Elts {
				field, fieldOK := element.(*ast.KeyValueExpr)
				if !fieldOK {
					continue
				}
				key, keyOK := field.Key.(*ast.Ident)
				value, valueOK := field.Value.(*ast.Ident)
				if keyOK && valueOK && key.Name == "Addr" && value.Name == "listen" {
					serverUsesListen = true
				}
			}
		}
		return true
	})
	if runtimeConfigCalls != 1 || rqliteRuntimeCalls != 1 || controlPlaneLiteralCount != 1 {
		t.Fatalf("pinned production main must select and construct the rqlite runtime exactly once")
	}
	if !listenBinding || !serverUsesListen {
		t.Fatal("production main does not bind the exact MAESTRO_LISTEN value to http.Server.Addr")
	}
}

func haTemplateRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if info, err := os.Stat(filepath.Join(root, "deploy", "ha")); err != nil || !info.IsDir() {
		t.Fatalf("repository root does not contain deploy/ha: %v", err)
	}
	return root
}

func haTemplateEnvironment(t *testing.T, path string) map[string]string {
	t.Helper()
	handle, err := os.Open(path)
	if err != nil {
		t.Fatalf("open panel environment template: %v", err)
	}
	defer handle.Close()

	environment := make(map[string]string)
	scanner := bufio.NewScanner(handle)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || key == "" || value == "" || key != strings.TrimSpace(key) || value != strings.TrimSpace(value) {
			t.Fatalf("invalid environment assignment")
		}
		if _, duplicate := environment[key]; duplicate {
			t.Fatalf("duplicate environment key %q", key)
		}
		environment[key] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan panel environment template: %v", err)
	}
	return environment
}

func haTemplateGetenv(environment map[string]string) func(string) string {
	return func(key string) string { return environment[key] }
}

func haTemplateSerializedEnvironment(environment map[string]string) string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(environment[key])
		builder.WriteByte('\n')
	}
	return builder.String()
}
