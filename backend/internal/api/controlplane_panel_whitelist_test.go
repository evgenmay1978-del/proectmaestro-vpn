package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type panelWhiteListTestBusiness struct {
	panelPermissionBusiness
	commands []panelWhiteListCommand
}

func (b *panelWhiteListTestBusiness) PanelWhiteListBalance(context.Context, string) (panelWhiteListView, error) {
	return panelWhiteListView{RemainingBytes: 3000000000}, nil
}
func (b *panelWhiteListTestBusiness) PanelWhiteListAdmin(_ context.Context, c panelWhiteListCommand) (panelWhiteListView, error) {
	b.commands = append(b.commands, c)
	return panelWhiteListView{Enabled: c.Action == "enable", RemainingBytes: c.GB * 1000000000}, nil
}

func TestPanelCDNActionsRequireSessionCSRFAndOwnerPermission(t *testing.T) {
	for _, action := range []string{"enable", "disable", "credit"} {
		t.Run(action, func(t *testing.T) {
			b := &panelWhiteListTestBusiness{}
			handler := NewControlPlane(b, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
			body := `{"login":"MiXeD","action":"` + action + `","gb":0}`
			if action == "credit" {
				body = `{"login":"MiXeD","action":"credit","gb":137}`
			}
			request := httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "manual-"+action)
			denied := httptest.NewRecorder()
			handler.ServeHTTP(denied, request)
			if denied.Code == http.StatusOK || len(b.commands) != 0 {
				t.Fatal("unauthenticated mutation")
			}
			request = httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "manual-"+action)
			request.Header.Set("X-CSRF", "panel-csrf")
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
			request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || len(b.commands) != 1 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			command := b.commands[0]
			if command.Login != "MiXeD" || command.Actor != "owner" || command.IdempotencyKey != "manual-"+action || b.authorize[len(b.authorize)-1].Permission != "settings.critical" {
				t.Fatalf("lost identity/permission: %#v", command)
			}
		})
	}
}

func TestPanelCDNCommandRejectsPackagesFractionalOrOutOfRangeAmounts(t *testing.T) {
	for _, c := range []panelWhiteListCommand{{Action: "credit", GB: 0}, {Action: "credit", GB: -1}, {Action: "credit", GB: 9223372037}, {Action: "enable", GB: 1}, {Action: "package", GB: 1}} {
		c.Login = "Exact"
		c.IdempotencyKey = "key"
		if validPanelWhiteListCommand(c) {
			t.Fatalf("invalid command: %#v", c)
		}
	}
}

func TestPanelCDNRejectsFractionalWireAndMissingCSRFBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		body string
		csrf bool
	}{
		{`{"login":"Exact","action":"credit","gb":1.5}`, true},
		{`{"login":"Exact","action":"credit","gb":1,"package":"fake"}`, true},
		{`{"login":"Exact","action":"credit","gb":1}`, false},
	} {
		b := &panelWhiteListTestBusiness{}
		handler := NewControlPlane(b, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
		request := httptest.NewRequest(http.MethodPost, "/mp/api/whitelist", strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "wire-amount")
		request.AddCookie(&http.Cookie{Name: controlPlanePanelCookie, Value: "panel-session"})
		request.AddCookie(&http.Cookie{Name: controlPlanePanelCSRFCookie, Value: "panel-csrf"})
		if test.csrf {
			request.Header.Set("X-CSRF", "panel-csrf")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusOK || len(b.commands) != 0 {
			t.Fatalf("invalid mutation accepted: %s", test.body)
		}
	}
}
