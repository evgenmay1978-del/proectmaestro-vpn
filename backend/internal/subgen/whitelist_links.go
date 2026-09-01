package subgen

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxWhiteListSubscriptionBytes = 1 << 20
	maxWhiteListLinkBytes         = 32 << 10
	mlkemClientEncryptionPrefix   = "mlkem768x25519plus.native.0rtt."
	mlkemClientMaterialBytes      = 1184
	canonicalXHTTPExtra           = `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id","uplinkHTTPMethod":"GET","uplinkDataPlacement":"body"}`
	whiteListShareLabel           = "MaestroVPN Yandex CDN"
)

var (
	errInvalidWhiteListNode          = errors.New("invalid whitelist node")
	errInvalidOrdinarySubscription   = errors.New("invalid ordinary subscription")
	errWhiteListSubscriptionTooLarge = errors.New("whitelist subscription too large")
)

// WhiteListShareLink returns a single modern-Xray VLESS/XHTTP URI. Internal
// release and entitlement identifiers are intentionally not serialized.
func WhiteListShareLink(node WhiteListNode) (string, error) {
	return whiteListShareLinkWithLabel(node, whiteListShareLabel)
}

func whiteListShareLinkWithLabel(node WhiteListNode, label string) (string, error) {
	extra, err := validatedWhiteListNodeExtra(node)
	if err != nil {
		return "", err
	}
	if !validWhiteListPublicLabel(label, node) {
		return "", errInvalidWhiteListNode
	}

	query := url.Values{
		"alpn":       []string{"h2"},
		"encryption": []string{node.Encryption},
		"extra":      []string{extra},
		"fp":         []string{node.Fingerprint},
		"host":       []string{node.Host},
		"mode":       []string{node.Mode},
		"path":       []string{node.Path},
		"security":   []string{node.Security},
		"sni":        []string{node.ServerName},
		"type":       []string{node.Network},
	}
	link := (&url.URL{
		Scheme:   "vless",
		User:     url.User(node.ClientID),
		Host:     net.JoinHostPort(node.Address, strconv.Itoa(node.Port)),
		RawQuery: query.Encode(),
		Fragment: label,
	}).String()
	if len(link) > maxWhiteListLinkBytes {
		return "", errWhiteListSubscriptionTooLarge
	}
	return link, nil
}

// AppendWhiteListShareLink preserves the decoded ordinary subscription as an
// exact prefix and appends at most one canonical whitelist URI.
func AppendWhiteListShareLink(encoded string, node WhiteListNode) (string, error) {
	node.Label = whiteListShareLabel
	return AppendWhiteListShareLinks(encoded, []WhiteListNode{node})
}

// AppendWhiteListShareLinks preserves an accepted ordinary document byte-for-byte
// and appends every new canonical whitelist URI only after the full batch is valid.
func AppendWhiteListShareLinks(encoded string, nodes []WhiteListNode) (string, error) {
	ordinary, err := decodeOrdinaryWhiteListSubscription(encoded)
	if err != nil {
		return "", err
	}
	if len(nodes) == 0 {
		return encoded, nil
	}

	links := make([]string, 0, len(nodes))
	labels := make(map[string]struct{}, len(nodes))
	clientIDs := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if _, exists := labels[node.Label]; exists {
			return "", errInvalidWhiteListNode
		}
		if _, exists := clientIDs[node.ClientID]; exists {
			return "", errInvalidWhiteListNode
		}
		link, err := whiteListShareLinkWithLabel(node, node.Label)
		if err != nil {
			return "", err
		}
		labels[node.Label] = struct{}{}
		clientIDs[node.ClientID] = struct{}{}
		links = append(links, link)
	}

	present := make(map[string]struct{}, len(links))
	for _, line := range bytes.Split(ordinary, []byte{'\n'}) {
		present[string(line)] = struct{}{}
	}
	appendBytes := 0
	for _, link := range links {
		if _, exists := present[link]; !exists {
			appendBytes += 1 + len(link)
		}
	}
	if len(ordinary)+appendBytes > maxWhiteListSubscriptionBytes {
		return "", errWhiteListSubscriptionTooLarge
	}

	augmented := make([]byte, 0, len(ordinary)+appendBytes)
	augmented = append(augmented, ordinary...)
	for _, link := range links {
		if _, exists := present[link]; exists {
			continue
		}
		augmented = append(augmented, '\n')
		augmented = append(augmented, link...)
		present[link] = struct{}{}
	}
	return base64.StdEncoding.EncodeToString(augmented), nil
}

func decodeOrdinaryWhiteListSubscription(encoded string) ([]byte, error) {
	if encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(maxWhiteListSubscriptionBytes) || strings.ContainsAny(encoded, " \t\r\n") {
		return nil, errInvalidOrdinarySubscription
	}
	ordinary, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(ordinary) == 0 || len(ordinary) > maxWhiteListSubscriptionBytes ||
		base64.StdEncoding.EncodeToString(ordinary) != encoded || !utf8.Valid(ordinary) ||
		bytes.ContainsAny(ordinary, "\x00\r") || ordinary[len(ordinary)-1] == '\n' {
		return nil, errInvalidOrdinarySubscription
	}
	return ordinary, nil
}

func validWhiteListPublicLabel(label string, node WhiteListNode) bool {
	if label == "" || len(label) > 255 || !utf8.ValidString(label) {
		return false
	}
	for _, char := range label {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	for _, internal := range []string{node.EdgeID, node.TransportProfileID, node.CompatibilityPresetID, node.TransportReleaseID} {
		if internal != "" && strings.Contains(label, internal) {
			return false
		}
	}
	return true
}

func validatedWhiteListNodeExtra(node WhiteListNode) (string, error) {
	if node.Protocol != "vless" || node.Network != "xhttp" || node.Port != 443 || !node.TLS ||
		node.Security != "tls" || node.Mode != "packet-up" || node.UplinkHTTPMethod != "GET" ||
		node.UplinkDataPlacement != "body" || node.Host != node.ServerName || !validWhiteListServerName(node.Host) ||
		!validWhiteListDialAddress(node.Address) || !validWhiteListPath(node.Path) || !validCanonicalUUID(node.ClientID) ||
		node.Fingerprint != "firefox" || len(node.ALPN) != 1 || node.ALPN[0] != "h2" ||
		!validMLKEMClientEncryption(node.Encryption) {
		return "", errInvalidWhiteListNode
	}
	extra, err := url.QueryUnescape(node.Extra)
	if err != nil || extra != canonicalXHTTPExtra || url.QueryEscape(extra) != node.Extra {
		return "", errInvalidWhiteListNode
	}
	return extra, nil
}

func validMLKEMClientEncryption(value string) bool {
	if !strings.HasPrefix(value, mlkemClientEncryptionPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, mlkemClientEncryptionPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == mlkemClientMaterialBytes && base64.RawURLEncoding.EncodeToString(decoded) == encoded
}

func validCanonicalUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' ||
		value[14] < '1' || value[14] > '8' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index, char := range []byte(value) {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func validWhiteListServerName(value string) bool {
	return net.ParseIP(value) == nil && validWhiteListDNSName(value)
}

func validWhiteListDialAddress(value string) bool {
	if ip := net.ParseIP(value); ip != nil {
		ipv4 := ip.To4()
		return ipv4 != nil && ip.String() == value && ip.IsGlobalUnicast() && !reservedWhiteListIPv4(ipv4[0], ipv4[1], ipv4[2])
	}
	if numericWhiteListAddress(value) {
		return false
	}
	return validWhiteListDNSName(value)
}

func reservedWhiteListIPv4(first, second, third byte) bool {
	switch {
	case first == 0, first == 10, first == 127, first >= 224:
		return true
	case first == 100 && second >= 64 && second <= 127:
		return true
	case first == 169 && second == 254:
		return true
	case first == 172 && second >= 16 && second <= 31:
		return true
	case first == 192 && second == 0 && (third == 0 || third == 2):
		return true
	case first == 192 && second == 88 && third == 99:
		return true
	case first == 192 && second == 168:
		return true
	case first == 198 && (second == 18 || second == 19 || (second == 51 && third == 100)):
		return true
	case first == 203 && second == 0 && third == 113:
		return true
	default:
		return false
	}
}

func numericWhiteListAddress(value string) bool {
	if !strings.Contains(value, ".") {
		return false
	}
	for _, char := range []byte(value) {
		if (char < '0' || char > '9') && char != '.' {
			return false
		}
	}
	return true
}

func validWhiteListDNSName(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range []byte(label) {
			if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
				return false
			}
		}
	}
	return true
}

func validWhiteListPath(value string) bool {
	if len(value) <= 1 || len(value) > 2048 || value[0] != '/' || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, "?#\\%\x00\r\n\t") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(value, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, char := range []byte(segment) {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == '~') {
				return false
			}
		}
	}
	return utf8.ValidString(value)
}
