package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var errInvalidRQLiteRuntime = errors.New("invalid rqlite runtime configuration")

type rqliteRuntimeConfig struct {
	Endpoints     []string
	CAFile        string
	CertFile      string
	KeyFile       string
	KeyBundleFile string
}

type configuredRuntimeFactories struct {
	legacy func(context.Context) (*panelRuntime, error)
	rqlite func(context.Context, rqliteRuntimeConfig) (*panelRuntime, error)
}

func buildConfiguredRuntime(
	ctx context.Context,
	getenv func(string) string,
	factories configuredRuntimeFactories,
) (*panelRuntime, error) {
	if getenv == nil || factories.legacy == nil || factories.rqlite == nil {
		return nil, errInvalidRQLiteRuntime
	}
	mode := strings.TrimSpace(getenv("MAESTRO_CONTROL_PLANE"))
	switch mode {
	case "", "legacy":
		return factories.legacy(ctx)
	case "rqlite":
		config, err := readRQLiteRuntimeConfig(getenv)
		if err != nil {
			return nil, err
		}
		return factories.rqlite(ctx, config)
	default:
		return nil, fmt.Errorf("unsupported MAESTRO_CONTROL_PLANE mode %q", mode)
	}
}

func readRQLiteRuntimeConfig(getenv func(string) string) (rqliteRuntimeConfig, error) {
	config := rqliteRuntimeConfig{
		CAFile:        strings.TrimSpace(getenv("MAESTRO_RQLITE_CA_FILE")),
		CertFile:      strings.TrimSpace(getenv("MAESTRO_RQLITE_CERT_FILE")),
		KeyFile:       strings.TrimSpace(getenv("MAESTRO_RQLITE_KEY_FILE")),
		KeyBundleFile: strings.TrimSpace(getenv("MAESTRO_RQLITE_KEY_BUNDLE_FILE")),
	}
	rawEndpoints := strings.Split(getenv("MAESTRO_RQLITE_ENDPOINTS"), ",")
	seen := make(map[string]struct{}, len(rawEndpoints))
	for _, rawEndpoint := range rawEndpoints {
		endpoint := strings.TrimSpace(rawEndpoint)
		if endpoint == "" {
			return rqliteRuntimeConfig{}, errInvalidRQLiteRuntime
		}
		if _, duplicate := seen[endpoint]; duplicate {
			return rqliteRuntimeConfig{}, errInvalidRQLiteRuntime
		}
		seen[endpoint] = struct{}{}
		config.Endpoints = append(config.Endpoints, endpoint)
	}
	if len(config.Endpoints) != 3 || config.CAFile == "" || config.CertFile == "" ||
		config.KeyFile == "" || config.KeyBundleFile == "" {
		return rqliteRuntimeConfig{}, errInvalidRQLiteRuntime
	}
	return config, nil
}
