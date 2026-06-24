package subgen

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// ShareLinks builds the universal "v2ray subscription": a base64-encoded list of
// vless:// / hysteria2:// / naive:// share-links that cross-platform clients
// (Karing, v2rayN, NekoBox, Streisand, Shadowrocket, …) import from a URL or QR.
// This is how non-Android customers (iPhone via Karing) get the subscription.
func ShareLinks(c Customer) string {
	var links []string
	if c.VLESS != nil {
		links = append(links, vlessLink(c.VLESS, c.Name))
	}
	if c.Hy2 != nil {
		links = append(links, hy2Link(c.Hy2, c.Name))
	}
	if c.Naive != nil {
		links = append(links, naiveLink(c.Naive, c.Name))
	}
	if c.AnyTLS != nil {
		links = append(links, anytlsLink(c.AnyTLS, c.Name))
	}
	if c.VLESS3 != nil {
		links = append(links, vlessLink(c.VLESS3, c.Name+" S3"))
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

func tag(proto, name string) string { return url.PathEscape("MaestroVPN " + proto + " " + name) }

func vlessLink(v *VLESSCreds, name string) string {
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("security", "reality")
	q.Set("sni", v.SNI)
	q.Set("fp", fallback(v.Fingerprint, "chrome"))
	q.Set("pbk", v.PublicKey)
	q.Set("sid", v.ShortID)
	q.Set("type", "tcp")
	if v.Flow != "" {
		q.Set("flow", v.Flow)
	}
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", v.UUID, v.Server, v.Port, q.Encode(), tag("VLESS", name))
}

func hy2Link(h *Hy2Creds, name string) string {
	q := url.Values{}
	q.Set("sni", h.SNI)
	if h.Insecure {
		q.Set("insecure", "1")
	}
	// userinfo encoding (RFC 3986): space → %20, NOT '+'. QueryEscape would emit
	// '+' which a client decodes as a literal plus, corrupting the password.
	auth := url.UserPassword(h.User, h.Pass).String()
	return fmt.Sprintf("hysteria2://%s@%s:%d?%s#%s", auth, h.Server, h.Port, q.Encode(), tag("Hy2", name))
}

func naiveLink(n *NaiveCreds, name string) string {
	auth := url.UserPassword(n.Username, n.Password).String()
	return fmt.Sprintf("naive+https://%s@%s:%d#%s", auth, n.Server, n.Port, tag("Naive", name))
}

func anytlsLink(a *AnyTLSCreds, name string) string {
	q := url.Values{}
	q.Set("sni", a.SNI)
	if a.Insecure {
		q.Set("insecure", "1")
	}
	// Password is hex (randHex) → no reserved chars, safe as raw userinfo.
	return fmt.Sprintf("anytls://%s@%s:%d?%s#%s", a.Password, a.Server, a.Port, q.Encode(), tag("AnyTLS", name))
}
