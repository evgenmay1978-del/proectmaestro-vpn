package api

import (
	"strings"
	"testing"
)

func TestPanelUIUsesBoundedPaginationPartialFailuresAndRealOrderStates(t *testing.T) {
	prefix, frozen, found := strings.Cut(panelHTML, "function renderOlc()")
	if !found || !strings.Contains(frozen, "function renderWDTT()") {
		t.Fatal("panel UI is missing the frozen OLCRTC/WDTT boundary")
	}
	for _, required := range []string{
		"Promise.allSettled",
		"next_cursor",
		"status:'created'",
		"status:'payment_claimed'",
		"payment_claimed",
		"Обработать просроченных",
		"вся база",
		"Записи клиентов из базы не удаляются",
		"Поиск на текущей странице",
		"read_ready",
		"write_readiness",
		"data_complete",
		"replication",
		"dns_tls",
		"failures",
	} {
		if !strings.Contains(prefix, required) {
			t.Errorf("panel UI prefix missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"CUST.filter(function(c){return !c.active&&!c.disabled;}).length",
		"Удалить истёкших",
		"S1+S3",
		"Promise.all([api('api/orders')",
	} {
		if strings.Contains(prefix, forbidden) {
			t.Errorf("panel UI prefix still contains unsafe legacy fragment %q", forbidden)
		}
	}
	if strings.Contains(frozen, "Promise.allSettled") || strings.Contains(frozen, "next_cursor") {
		t.Fatal("pagination/resilience edit crossed into frozen OLCRTC/WDTT code")
	}
}
