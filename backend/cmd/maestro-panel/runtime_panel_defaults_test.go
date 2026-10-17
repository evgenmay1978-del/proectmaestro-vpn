package main

import "testing"

func TestRQLiteAPIConfigDefaultsPanelPasswordFile(t *testing.T) {
	t.Setenv("MAESTRO_PANEL_PW_FILE", "")

	config := rqliteAPIConfigFromEnvironment()
	if config.PanelPWFile != "/var/lib/maestro/panel-pw.hash" {
		t.Fatalf("PanelPWFile=%q, want /var/lib/maestro/panel-pw.hash", config.PanelPWFile)
	}
}
