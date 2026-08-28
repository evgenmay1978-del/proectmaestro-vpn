package api

import (
	"encoding/json"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type subscriptionRenderOptions struct{}

func renderControlPlaneSubscription(customer controlplane.BusinessCustomer, _ subgen.Customer, _ subscriptionRenderOptions) (json.RawMessage, string, error) {
	document, err := json.Marshal(map[string]any{
		"login": customer.Login, "credentials": customer.Access.Credentials,
	})
	return document, "application/json", err
}
