package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	incylink "github.com/INCY-DEV/incy-link-encoder/go"
)

type adminDeliveryBusiness struct {
	Business
	view CustomerView
}

func (b adminDeliveryBusiness) CustomerByLogin(context.Context, string) (CustomerView, error) {
	return b.view, nil
}

func TestAdminCustomerReturnsCanonicalDeliveryChoices(t *testing.T) {
	const bare = "https://sub.example.com/sub/fixture-token"
	const links = bare + "?format=links"
	server := NewControlPlane(adminDeliveryBusiness{view: CustomerView{Login: "fixture", SubURL: bare}}, Config{})
	recorder := httptest.NewRecorder()
	server.handleControlPlaneCustomer(recorder, httptest.NewRequest(http.MethodGet, "/admin/customer?login=fixture", nil))

	var response struct {
		SubURL     string                            `json:"sub_url"`
		Deliveries map[string]CommercialDeliveryView `json:"deliveries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode admin customer response: %v", err)
	}
	if recorder.Code != http.StatusOK || response.SubURL != bare {
		t.Fatalf("admin customer status=%d sub_url=%q", recorder.Code, response.SubURL)
	}
	if response.Deliveries["happ"].URL != links {
		t.Fatalf("Happ URL = %q, want %q", response.Deliveries["happ"].URL, links)
	}
	decoded, err := incylink.DecryptLink(response.Deliveries["incy"].URL)
	if err != nil || decoded.URL != links {
		t.Fatalf("Incy descriptor did not retain stable links URL: decoded=%#v err=%v", decoded, err)
	}
	const wantKaring = "karing://install-config?url=https%3A%2F%2Fsub.example.com%2Fsub%2Ffixture-token%3Fformat%3Dlinks&name=MaestroVPN"
	if response.Deliveries["karing"].URL != wantKaring {
		t.Fatalf("Karing URL = %q, want %q", response.Deliveries["karing"].URL, wantKaring)
	}
}
