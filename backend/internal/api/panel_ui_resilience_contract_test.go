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

func TestPanelCDNRetryJournalDistinguishesRejectedAndUnknownOperations(t *testing.T) {
	start := strings.Index(panelHTML, "function bindCustomerCDN(login)")
	end := strings.Index(panelHTML, "function changePwDlg()")
	if start < 0 || end <= start {
		t.Fatal("missing CDN dialog boundary")
	}
	dialog := panelHTML[start:end]
	for _, required := range []string{
		"var alreadyUncertain=!!pending.uncertain;pending.uncertain=true",
		"var rejected=!alreadyUncertain&&([400,403,404].indexOf(e.status)>=0||(e.status===409&&e.message==='controlplane: conflict'))",
		"if(rejected){sessionStorage.removeItem(storageKey);pending=null",
		"'Idempotency-Key':pending.key",
		"body:JSON.stringify(pending.command)",
	} {
		if !strings.Contains(dialog, required) {
			t.Fatalf("missing retry contract %q", required)
		}
	}
	if !strings.Contains(panelHTML, "error.status=r.status;throw error") {
		t.Fatal("HTTP errors lose status")
	}
	if strings.Index(dialog, "pending.uncertain=true") > strings.Index(dialog, "api('api/whitelist',") {
		t.Fatal("uncertainty was persisted after sending")
	}
}
