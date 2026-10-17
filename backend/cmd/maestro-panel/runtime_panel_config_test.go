package main

import "testing"

func TestRQLiteAPIConfigCarriesPanelCredentials(t *testing.T) {
	t.Setenv("MAESTRO_PANEL_PATH", "/panel-secret/")
	t.Setenv("MAESTRO_PANEL_PASSWORD_HASH", "bootstrap-hash")
	t.Setenv("MAESTRO_PANEL_PW_FILE", "/run/maestro/panel-password.hash")

	config := rqliteAPIConfigFromEnvironment()
	if config.PanelPath != "/panel-secret/" {
		t.Fatalf("PanelPath = %q", config.PanelPath)
	}
	if config.PanelPasswordHash != "bootstrap-hash" {
		t.Fatalf("PanelPasswordHash = %q, want bootstrap hash", config.PanelPasswordHash)
	}
	if config.PanelPWFile != "/run/maestro/panel-password.hash" {
		t.Fatalf("PanelPWFile = %q, want configured runtime password file", config.PanelPWFile)
	}
}
