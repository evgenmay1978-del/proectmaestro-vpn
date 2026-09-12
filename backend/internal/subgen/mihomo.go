package subgen

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// MihomoSubscription renders an owned subscription without an external converter.
// JSON is a YAML subset accepted by Mihomo. Ordinary nodes stay the default;
// paid CDN nodes can only be chosen explicitly.
func MihomoSubscription(encoded string) ([]byte, error) {
	raw, err := decodeOrdinaryWhiteListSubscription(encoded)
	if err != nil { return nil, err }
	proxies := []map[string]any{}
	ordinary, cdn := []string{}, []string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		u, err := url.Parse(line)
		if err != nil { return nil, errInvalidOrdinarySubscription }
		kind := strings.ToLower(u.Scheme)
		if kind != "vless" && kind != "hysteria2" && kind != "hy2" { continue }
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || u.User == nil || u.Hostname() == "" || u.Fragment == "" || seen[u.Fragment] {
			return nil, errInvalidOrdinarySubscription
		}
		seen[u.Fragment] = true
		q := u.Query()
		p := map[string]any{"name":u.Fragment,"type":kind,"server":u.Hostname(),"port":port,"udp":true}
		isCDN := kind == "vless" && q.Get("type") == "xhttp"
		if kind == "vless" {
			p["uuid"] = u.User.Username()
			p["network"] = q.Get("type")
			p["tls"] = q.Get("security") == "tls" || q.Get("security") == "reality"
			p["servername"] = q.Get("sni")
			p["client-fingerprint"] = q.Get("fp")
			if q.Get("flow") != "" { p["flow"] = q.Get("flow") }
			if q.Get("encryption") != "" && q.Get("encryption") != "none" { p["encryption"] = q.Get("encryption") }
			if q.Get("security") == "reality" { p["reality-opts"] = map[string]string{"public-key":q.Get("pbk"),"short-id":q.Get("sid")} }
			if isCDN {
				var extra map[string]any
				if json.Unmarshal([]byte(q.Get("extra")), &extra) != nil { return nil, errInvalidWhiteListNode }
				x := map[string]any{"path":q.Get("path"),"host":q.Get("host"),"mode":q.Get("mode")}
				for from, to := range map[string]string{"sessionIDPlacement":"session-placement","sessionIDKey":"session-key","seqPlacement":"seq-placement","seqKey":"seq-key","uplinkHTTPMethod":"uplink-http-method","uplinkDataPlacement":"uplink-data-placement"} {
					v, ok := extra[from].(string); if !ok || v == "" { return nil, errInvalidWhiteListNode }; x[to] = v
				}
				p["xhttp-opts"] = x
				p["alpn"] = []string{"h2"}
			}
		} else {
			p["type"] = "hysteria2"
			password := u.User.Username()
			if pass, ok := u.User.Password(); ok { password += ":"+pass }
			p["password"] = password
			p["sni"] = q.Get("sni")
			if q.Get("obfs") != "" { p["obfs"] = q.Get("obfs"); p["obfs-password"] = q.Get("obfs-password") }
		}
		proxies = append(proxies,p)
		if isCDN { cdn = append(cdn,u.Fragment) } else { ordinary = append(ordinary,u.Fragment) }
	}
	if len(ordinary) == 0 { return nil, errInvalidOrdinarySubscription }
	groups := []map[string]any{{"name":"MaestroVPN","type":"select","proxies":append(append([]string{},ordinary...),cdn...)}}
	config := map[string]any{
		"mixed-port":7890,"allow-lan":false,"mode":"rule","log-level":"warning",
		"dns":map[string]any{"enable":true,"enhanced-mode":"fake-ip","nameserver":[]string{"https://1.1.1.1/dns-query","https://dns.google/dns-query"}},
		"proxies":proxies,"proxy-groups":groups,"rules":[]string{"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve","IP-CIDR,10.0.0.0/8,DIRECT,no-resolve","IP-CIDR,172.16.0.0/12,DIRECT,no-resolve","IP-CIDR,192.168.0.0/16,DIRECT,no-resolve","MATCH,MaestroVPN"},
	}
	return json.Marshal(config)
}
