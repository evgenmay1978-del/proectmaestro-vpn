package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestControlPlanePanelOrderCursorIsBoundToExactFilter(t *testing.T) {
	business := &panelRatePaginationBusiness{orders: []OrderView{
		{OrderID: "order-c", Status: "pending", CreatedAtUnix: 300},
		{OrderID: "order-b", Status: "pending", CreatedAtUnix: 200},
	}}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	first := panelPaginationGET(t, handler, "/mp/api/orders?limit=1")
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusOK || page.NextCursor == "" {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}

	mismatched := panelPaginationGET(t, handler, "/mp/api/orders?limit=1&status=pending&cursor="+url.QueryEscape(page.NextCursor))
	if mismatched.Code != http.StatusBadRequest {
		t.Fatalf("filter-mismatched cursor status=%d body=%s, want 400", mismatched.Code, mismatched.Body.String())
	}
}

func TestControlPlanePanelCustomerProtocolsSerializeAsEmptyArray(t *testing.T) {
	business := &panelRatePaginationBusiness{customers: []CustomerView{{CustomerID: "customer-a", Login: "alice"}}}
	handler := NewControlPlane(business, Config{PanelPath: "/mp/", PanelPasswordHash: "configured"}).Handler()
	response := panelPaginationGET(t, handler, "/mp/api/customers?limit=1")
	var page struct {
		Customers []struct {
			Protocols []string `json:"protocols"`
		} `json:"customers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(page.Customers) != 1 {
		t.Fatalf("customers status=%d body=%s", response.Code, response.Body.String())
	}
	if page.Customers[0].Protocols == nil || len(page.Customers[0].Protocols) != 0 {
		t.Fatalf("protocols=%#v body=%s, want non-nil empty array", page.Customers[0].Protocols, response.Body.String())
	}
}
