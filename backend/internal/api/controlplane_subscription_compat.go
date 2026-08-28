package api

import (
	"encoding/json"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type subscriptionRenderOptions struct{}

func renderControlPlaneSubscription(customer controlplane.BusinessCustomer, topology subgen.Customer, _ subscriptionRenderOptions) (json.RawMessage, string, error) {
	configured := topology
	configured.Name = customer.Login
	configured.VLESS = configuredVLESS(topology.VLESS, customer.Access.Credentials["vless"])
	configured.Hy2 = configuredHy2(topology.Hy2, customer.Login, customer.Access.Credentials["hysteria2"])
	configured.Naive = configuredNaive(topology.Naive, customer.Login, customer.Access.Credentials["naive"])
	configured.AnyTLS = configuredAnyTLS(topology.AnyTLS, customer.Access.Credentials["anytls"])
	document, err := subgen.GenerateSingbox(configured)
	return json.RawMessage(document), "application/json", err
}

func configuredVLESS(topology *subgen.VLESSCreds, uuid string) *subgen.VLESSCreds {
	if topology == nil || uuid == "" {
		return nil
	}
	configured := *topology
	configured.UUID = uuid
	return &configured
}

func configuredHy2(topology *subgen.Hy2Creds, login, password string) *subgen.Hy2Creds {
	if topology == nil || password == "" {
		return nil
	}
	configured := *topology
	configured.User = login
	configured.Pass = password
	return &configured
}

func configuredNaive(topology *subgen.NaiveCreds, login, password string) *subgen.NaiveCreds {
	if topology == nil || password == "" {
		return nil
	}
	configured := *topology
	configured.Username = login
	configured.Password = password
	return &configured
}

func configuredAnyTLS(topology *subgen.AnyTLSCreds, password string) *subgen.AnyTLSCreds {
	if topology == nil || password == "" {
		return nil
	}
	configured := *topology
	configured.Password = password
	return &configured
}
