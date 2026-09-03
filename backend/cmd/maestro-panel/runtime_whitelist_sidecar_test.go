package main

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/sidecaragentclient"
)

func TestRuntimeWhiteListSidecarDefaultsDisabled(t *testing.T) {
	config, err := readRuntimeWhiteListSidecarConfig(func(string) string { return "" })
	if err != nil || config.Enabled || len(config.Nodes) != 0 {
		t.Fatalf("config=%#v err=%v", config, err)
	}
	senders, err := buildRuntimeWhiteListSidecarSenders(config, func(sidecaragentclient.Config) (controlplane.ExternalActionSender, error) {
		t.Fatal("disabled runtime constructed a client")
		return nil, nil
	})
	if err != nil || len(senders) != 0 {
		t.Fatalf("senders=%#v err=%v", senders, err)
	}
}

func TestRuntimeWhiteListSidecarBuildsOnlyExactFourMTLSNodeClients(t *testing.T) {
	values := map[string]string{
		"MAESTRO_WHITELIST_SIDECAR_ENABLE":    "1",
		"MAESTRO_WHITELIST_SIDECAR_CA_FILE":   "/run/maestro-sidecar/ca.pem",
		"MAESTRO_WHITELIST_SIDECAR_CERT_FILE": "/run/maestro-sidecar/client.pem",
		"MAESTRO_WHITELIST_SIDECAR_KEY_FILE":  "/run/maestro-sidecar/client.key",
	}
	for _, node := range []string{"S1", "S2", "S3", "S4"} {
		values["MAESTRO_WHITELIST_SIDECAR_"+node+"_URL"] = "https://192.0.2." + node[1:] + ":18443"
		values["MAESTRO_WHITELIST_SIDECAR_"+node+"_SERVER_NAME"] = "sidecar-" + node + ".maestro.invalid"
	}
	config, err := readRuntimeWhiteListSidecarConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatalf("readRuntimeWhiteListSidecarConfig: %v", err)
	}
	seen := map[string]sidecaragentclient.Config{}
	senders, err := buildRuntimeWhiteListSidecarSenders(config, func(clientConfig sidecaragentclient.Config) (controlplane.ExternalActionSender, error) {
		seen[clientConfig.ServerName] = clientConfig
		return runtimeExternalSender{}, nil
	})
	if err != nil || len(senders) != 4 || len(seen) != 4 {
		t.Fatalf("senders=%d configs=%d err=%v", len(senders), len(seen), err)
	}
	if !reflect.DeepEqual(runtimeSenderKeys(senders), []string{"s1", "s2", "s3", "s4"}) {
		t.Fatalf("nodes=%#v", runtimeSenderKeys(senders))
	}
	for serverName, clientConfig := range seen {
		if clientConfig.CAFile != values["MAESTRO_WHITELIST_SIDECAR_CA_FILE"] ||
			clientConfig.CertFile != values["MAESTRO_WHITELIST_SIDECAR_CERT_FILE"] ||
			clientConfig.KeyFile != values["MAESTRO_WHITELIST_SIDECAR_KEY_FILE"] ||
			clientConfig.ServerName != serverName || clientConfig.RequestTimeout != 5*time.Second ||
			clientConfig.ReceiptLookupTimeout != 2*time.Second {
			t.Fatalf("client config=%#v", clientConfig)
		}
	}
}

func TestRuntimeWhiteListSidecarRejectsPartialOrNonTLSConfiguration(t *testing.T) {
	base := map[string]string{
		"MAESTRO_WHITELIST_SIDECAR_ENABLE":    "1",
		"MAESTRO_WHITELIST_SIDECAR_CA_FILE":   "/run/maestro-sidecar/ca.pem",
		"MAESTRO_WHITELIST_SIDECAR_CERT_FILE": "/run/maestro-sidecar/client.pem",
		"MAESTRO_WHITELIST_SIDECAR_KEY_FILE":  "/run/maestro-sidecar/client.key",
	}
	for _, node := range []string{"S1", "S2", "S3", "S4"} {
		base["MAESTRO_WHITELIST_SIDECAR_"+node+"_URL"] = "https://192.0.2." + node[1:] + ":18443"
		base["MAESTRO_WHITELIST_SIDECAR_"+node+"_SERVER_NAME"] = "sidecar-" + node + ".maestro.invalid"
	}
	for name, mutate := range map[string]func(map[string]string){
		"missing node": func(values map[string]string) { delete(values, "MAESTRO_WHITELIST_SIDECAR_S4_URL") },
		"plaintext":    func(values map[string]string) { values["MAESTRO_WHITELIST_SIDECAR_S2_URL"] = "http://192.0.2.2:18443" },
		"embedded auth": func(values map[string]string) {
			values["MAESTRO_WHITELIST_SIDECAR_S3_URL"] = "https://user:pass@192.0.2.3:18443"
		},
	} {
		t.Run(name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			mutate(values)
			if _, err := readRuntimeWhiteListSidecarConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("invalid runtime configuration accepted")
			}
		})
	}
}

func TestRuntimeWhiteListSidecarFailsClosedWhenAnyClientCannotBeBuilt(t *testing.T) {
	config := runtimeWhiteListSidecarConfig{Enabled: true, Nodes: map[string]sidecaragentclient.Config{
		"s1": {BaseURL: "https://192.0.2.1:18443", ServerName: "sidecar-s1.maestro.invalid"},
	}}
	want := errors.New("synthetic client failure")
	if senders, err := buildRuntimeWhiteListSidecarSenders(config, func(sidecaragentclient.Config) (controlplane.ExternalActionSender, error) {
		return nil, want
	}); !errors.Is(err, want) || senders != nil {
		t.Fatalf("senders=%#v err=%v", senders, err)
	}
}

type runtimeExternalSender struct{}

func (runtimeExternalSender) Post(_ context.Context, _ []byte) ([]byte, error) { return nil, nil }

func runtimeSenderKeys(senders map[string]controlplane.ExternalActionSender) []string {
	keys := make([]string, 0, len(senders))
	for key := range senders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
