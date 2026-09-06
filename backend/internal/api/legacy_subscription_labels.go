package api

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type legacyShareLabeler func(string) (string, error)

type legacyShareEndpoint struct {
	scheme string
	host   string
	port   string
}

// Country facts were resolved for the current S1..S4 exits on 2026-09-06:
// S1 ES (RIPE, Madrid), S2 CZ (smartape geofeed), S3 NL (ehostiron
// geofeed), S4 DE (Snowd geofeed, Frankfurt). Match the configured service
// endpoint, never a customer's name or an unverified hostname suffix.
func newLegacyShareLabeler(topology subgen.Customer) legacyShareLabeler {
	labels := make(map[legacyShareEndpoint]string)
	add := func(scheme, host string, port int, label string) {
		if host == "" || port <= 0 || port > 65535 {
			return
		}
		endpoint := legacyShareEndpoint{scheme: scheme, host: strings.ToLower(host), port: strconv.Itoa(port)}
		if previous, exists := labels[endpoint]; exists && previous != label {
			// Ambiguous topology cannot authorize a geographical label.
			labels[endpoint] = ""
			return
		}
		labels[endpoint] = label
	}
	if node := topology.VLESS; node != nil {
		add("vless", node.Server, node.Port, "🇪🇸 Испания · VLESS")
	}
	if node := topology.Hy2; node != nil {
		add("hysteria2", node.Server, node.Port, "🇨🇿 Чехия · Hysteria2")
	}
	if node := topology.Naive; node != nil {
		add("naive+https", node.Server, node.Port, "🇨🇿 Чехия · Naive")
	}
	if node := topology.AnyTLS; node != nil {
		add("anytls", node.Server, node.Port, "🇨🇿 Чехия · AnyTLS")
	}
	if node := topology.VLESS3; node != nil {
		add("vless", node.Server, node.Port, "🇳🇱 Нидерланды · VLESS")
	}
	if node := topology.VLESS4; node != nil {
		add("vless", node.Server, node.Port, "🇩🇪 Германия · VLESS")
	}
	if len(labels) == 0 {
		return nil
	}
	return func(encoded string) (string, error) {
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return encoded, err
		}
		lines := strings.Split(string(decoded), "\n")
		changed := false
		for index, line := range lines {
			transport, _, hasFragment := strings.Cut(line, "#")
			if !hasFragment {
				continue
			}
			parsed, err := url.Parse(transport)
			if err != nil {
				continue
			}
			label := labels[legacyShareEndpoint{
				scheme: parsed.Scheme, host: strings.ToLower(parsed.Hostname()), port: parsed.Port(),
			}]
			if label == "" {
				continue
			}
			// Do not re-serialize the parsed URL: credentials, address, query,
			// transport options and their escaping stay byte-for-byte intact.
			renamed := transport + "#" + url.PathEscape(label)
			if renamed != line {
				lines[index] = renamed
				changed = true
			}
		}
		if !changed {
			return encoded, nil
		}
		return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n"))), nil
	}
}
