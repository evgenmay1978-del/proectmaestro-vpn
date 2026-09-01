package main

import (
	"os"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// Publication is deliberately disconnected from runtime until a later gated task.
func runtimeWhiteListPublicationSource() api.WhiteListPublicationSource {
	return runtimeWhiteListPublicationSourceFrom(nil, false)
}

// runtimeWhiteListPublicationSourceFrom is the explicit injection seam used by
// tests and by a later, separately gated runtime source. Publication remains
// fail-closed unless enable is true and a source is supplied.
func runtimeWhiteListPublicationSourceFrom(source api.WhiteListPublicationSource, enable bool) api.WhiteListPublicationSource {
	if !enable {
		return nil
	}
	return source
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
