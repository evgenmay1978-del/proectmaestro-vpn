package api

import (
	"context"
	"encoding/json"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type subscriptionRenderOptions struct {
	ClientRequest      bool
	UserAgent          string
	Links              bool
	Endpoint           subscriptionEndpointKind
	DeviceID           string
	EnforceDeviceLimit bool
}

type subscriptionEndpointKind string

const (
	subscriptionEndpointBase    subscriptionEndpointKind = "base"
	subscriptionEndpointHelpers subscriptionEndpointKind = "helpers"
	subscriptionEndpointInfo    subscriptionEndpointKind = "info"
)

func (options subscriptionRenderOptions) endpoint() subscriptionEndpointKind {
	if options.Endpoint == "" {
		return subscriptionEndpointBase
	}
	return options.Endpoint
}

// The optional request-aware adapter leaves the frozen Business port and
// non-HTTP subscription consumers source-compatible.
type requestSubscriptionSource interface {
	subscriptionSnapshotForRequest(context.Context, string, subscriptionRenderOptions) (SubscriptionSnapshot, error)
}

func renderControlPlaneSubscription(customer controlplane.BusinessCustomer, topology subgen.Customer, options subscriptionRenderOptions) (json.RawMessage, string, error) {
	configured := topology
	configured.Name = customer.Login
	configured.VLESS = configuredVLESS(topology.VLESS, customer.Access.Credentials["vless"])
	configured.Hy2 = configuredHy2(topology.Hy2, customer.Login, customer.Access.Credentials["hysteria2"])
	naiveUsername := customer.Login
	if importedUsername := customer.Access.CredentialUsernames["naive"]; importedUsername != "" {
		naiveUsername = importedUsername
	}
	configured.Naive = configuredNaive(topology.Naive, naiveUsername, customer.Access.Credentials["naive"])
	configured.AnyTLS = configuredAnyTLS(topology.AnyTLS, customer.Access.Credentials["anytls"])
	// Frozen provisionS3/provisionS4 reuse the customer's primary VLESS UUID.
	configured.VLESS3 = configuredVLESS(topology.VLESS3, customer.Access.Credentials["vless"])
	configured.VLESS4 = configuredVLESS(topology.VLESS4, customer.Access.Credentials["vless"])
	configured.WG = nil
	if raw := customer.Access.Credentials["awg"]; raw != "" {
		var err error
		configured.WG, err = controlplane.DecodeWGCredentialIdentity(raw)
		if err != nil {
			return nil, "", err
		}
	}
	// Match the legacy HTTP handler for links, old/non-app clients, and normal
	// requests. A saved WG tuple alone never enables an incompatible engine.
	if configured.WG != nil && appVersionCode(options.UserAgent) < awgMinVC {
		configured.WG = nil
	}
	if options.ClientRequest {
		configured.DNSFakeIP = !dnsFakeIPOff
	}
	// Share links retain Naive even for third-party clients: each link is
	// independently usable. The Cronet gate applies only to a sing-box document.
	if options.Links {
		return json.RawMessage(subgen.ShareLinks(configured)), "text/plain; charset=utf-8", nil
	}
	if options.ClientRequest && appVersionCode(options.UserAgent) == 0 {
		configured.Naive = nil
	}
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
