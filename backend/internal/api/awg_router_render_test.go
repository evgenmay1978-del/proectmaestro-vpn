package api

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

// Сквозь настоящий рендер подписки: роутер с меткой движка получает awg-endpoint клиента (и в urltest «auto»),
// роутер без метки / curl — нет. Именно то, что видит update.sh роутера.
func TestRenderRouterAwgByUserAgent(t *testing.T) {
	wg := &subgen.WGCreds{Server: "router-wg.example.test", Port: 443,
		PeerPublicKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)),
		PrivateKey:    base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)), LocalAddress: "10.10.8.77/32"}
	raw, err := controlplane.EncodeWGCredentialIdentity(wg)
	if err != nil {
		t.Fatal(err)
	}
	customer := controlplane.BusinessCustomer{Login: "fixture", Customer: controlplane.Customer{Access: controlplane.CustomerAccess{
		Credentials: map[string]string{"awg": raw, "anytls": "ordinary-account-password"}}}}
	topology := subgen.Customer{AnyTLS: &subgen.AnyTLSCreds{Server: "ordinary.example.test", Port: 443}}
	for _, c := range []struct {
		ua   string
		want bool
	}{
		{"MaestroVPN-Router/0.1.31 (sing-box 1.12.25-mvpn-awg31; awg)", true},
		{"MaestroVPN-Router/0.1.30", false},
		{"curl/8.12.1", false},
	} {
		document, _, err := renderControlPlaneSubscription(customer, topology, subscriptionRenderOptions{ClientRequest: true, UserAgent: c.ua})
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(document, []byte(wg.Server)); got != c.want {
			t.Fatalf("UA %q: awg в подписке = %v, ожидал %v", c.ua, got, c.want)
		}
		if c.want && !bytes.Contains(document, []byte(`"type":"awg"`)) && !bytes.Contains(document, []byte(`"type": "awg"`)) {
			t.Fatalf("UA %q: нет endpoint типа awg", c.ua)
		}
	}
}
