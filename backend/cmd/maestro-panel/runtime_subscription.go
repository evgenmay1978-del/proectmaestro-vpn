package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

const runtimeWhiteListXHTTPExtra = `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id","uplinkHTTPMethod":"GET","uplinkDataPlacement":"body"}`

type rqliteWhiteListPublicationSource struct {
	service *controlplane.Service
}

func runtimeWhiteListPublicationSource(service *controlplane.Service, enable bool) api.WhiteListPublicationSource {
	if !enable || service == nil {
		return nil
	}
	return rqliteWhiteListPublicationSource{service: service}
}

func (s rqliteWhiteListPublicationSource) WhiteListPublication(
	ctx context.Context, token string, now time.Time,
) (api.WhiteListPublicationSnapshot, error) {
	delivery, err := s.service.WhiteListPublicationDelivery(ctx, token, now)
	if err != nil {
		return api.WhiteListPublicationSnapshot{}, err
	}
	snapshot := api.WhiteListPublicationSnapshot{
		Verdict:           api.WhiteListPublicationVerdict(delivery.Decision.Verdict),
		ProjectionVersion: delivery.Decision.ProjectionVersion,
		DesiredGeneration: delivery.Decision.DesiredGeneration,
	}
	if delivery.Decision.FreshUntilUnix > 0 {
		snapshot.FreshThrough = time.Unix(delivery.Decision.FreshUntilUnix, 0)
	}
	if delivery.Decision.Verdict != controlplane.WhiteListPublicationPublishable {
		return snapshot, nil
	}
	snapshot.Nodes = []subgen.WhiteListNode{{
		Protocol: "vless", Network: "xhttp", Address: delivery.Material.PublicHost, Port: 443,
		TLS: true, ServerName: delivery.Material.PublicHost, Host: delivery.Material.PublicHost,
		Path: delivery.Material.SecretPath, Mode: "packet-up", UplinkHTTPMethod: "GET",
		UplinkDataPlacement: "body", ClientID: delivery.Material.ClientID,
		Encryption: delivery.Material.ClientEncryption, Security: "tls",
		ALPN: []string{"h2", "http/1.1"}, Fingerprint: "firefox",
		Extra:          url.QueryEscape(runtimeWhiteListXHTTPExtra),
		Label:          fmt.Sprintf("Maestro CDN — %s", delivery.CountryLabel),
		DomainFallback: true, TransportProfileID: delivery.ProfileID,
		CompatibilityPresetID: delivery.PresetID, TransportReleaseID: delivery.ReleaseID,
	}}
	return snapshot, nil
}

// Keep the frozen legacy provisioning topology without constructing its JSON
// stores, panel clients, Provisioner, or SSH dependencies in rqlite mode. Only
// public endpoint parameters live here; customer access comes from the cluster.
func rqliteSubscriptionTopologyFromEnvironment() subgen.Customer {
	topology := subgen.Customer{
		VLESS: runtimeVLESSTopology("", env("VLESS_SERVER", "wapmixx.ru")),
		Hy2: &subgen.Hy2Creds{
			Server: env("HY2_SERVER", "wapmix.duckdns.org"), Port: atoi(os.Getenv("S2_HY2_PORT"), 8443),
			SNI: env("HY2_SNI", "wapmix.duckdns.org"), Insecure: env("HY2_INSECURE", "1") == "1",
		},
	}
	if server := os.Getenv("NAIVE_SERVER"); server != "" {
		topology.Naive = &subgen.NaiveCreds{
			Server: server, Port: atoi(os.Getenv("NAIVE_PORT"), 443), SNI: env("NAIVE_SNI", server),
		}
	}
	if server := os.Getenv("ANYTLS_SERVER"); server != "" {
		topology.AnyTLS = &subgen.AnyTLSCreds{
			Server: server, Port: atoi(os.Getenv("ANYTLS_PORT"), 8443),
			SNI: env("ANYTLS_SNI", server), Insecure: env("ANYTLS_INSECURE", "1") == "1",
		}
	}
	// Preserve the legacy two-variable node enablement gates. These are only
	// presence checks; no connection to either legacy panel is made.
	if os.Getenv("S3_XUI_BASE_URL") != "" && os.Getenv("S3_VLESS_SERVER") != "" {
		topology.VLESS3 = runtimeVLESSTopology("S3_", os.Getenv("S3_VLESS_SERVER"))
	}
	if os.Getenv("S4_XUI_BASE_URL") != "" && os.Getenv("S4_VLESS_SERVER") != "" {
		topology.VLESS4 = runtimeVLESSTopology("S4_", os.Getenv("S4_VLESS_SERVER"))
	}
	return topology
}

func runtimeVLESSTopology(prefix, server string) *subgen.VLESSCreds {
	return &subgen.VLESSCreds{
		Server: server, Port: atoi(os.Getenv(prefix+"VLESS_PORT"), 443),
		SNI: os.Getenv(prefix + "VLESS_SNI"), PublicKey: os.Getenv(prefix + "VLESS_PBK"),
		ShortID: os.Getenv(prefix + "VLESS_SID"), Flow: env(prefix+"VLESS_FLOW", "xtls-rprx-vision"),
		Fingerprint: env(prefix+"VLESS_FP", "chrome"),
	}
}
