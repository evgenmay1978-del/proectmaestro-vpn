package canary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
)

type Artifacts struct{ serverConfig, directClientConfig, cdnClientConfig, clientURI, receipt []byte }

func (a Artifacts) ServerConfig() []byte       { return append([]byte(nil), a.serverConfig...) }
func (a Artifacts) DirectClientConfig() []byte { return append([]byte(nil), a.directClientConfig...) }
func (a Artifacts) CDNClientConfig() []byte    { return append([]byte(nil), a.cdnClientConfig...) }
func (a Artifacts) ClientURI() []byte          { return append([]byte(nil), a.clientURI...) }
func (a Artifacts) Receipt() []byte            { return append([]byte(nil), a.receipt...) }

func (s Snapshot) Materialize() (Artifacts, error) {
	if _, err := ParseSnapshot(s.CanonicalJSON()); err != nil {
		return Artifacts{}, err
	}
	server, err := json.Marshal(serverConfig(s))
	if err != nil {
		return Artifacts{}, invalid("config_encode")
	}
	direct, err := json.Marshal(clientConfig(s, "127.0.0.1", 18081, "none", 10808))
	if err != nil {
		return Artifacts{}, invalid("config_encode")
	}
	cdn, err := json.Marshal(clientConfig(s, s.Request.PublicHost, 443, "tls", 10809))
	if err != nil {
		return Artifacts{}, invalid("config_encode")
	}
	uri := []byte(clientURI(s))
	receipt, err := json.Marshal(struct {
		SchemaVersion            int      `json:"schema_version"`
		SnapshotSHA256           string   `json:"snapshot_sha256"`
		ServerConfigSHA256       string   `json:"server_config_sha256"`
		DirectClientConfigSHA256 string   `json:"direct_client_config_sha256"`
		CDNClientConfigSHA256    string   `json:"cdn_client_config_sha256"`
		ClientURISHA256          string   `json:"client_uri_sha256"`
		ReasonCodes              []string `json:"reason_codes"`
	}{1, s.SHA256(), sha(server), sha(direct), sha(cdn), sha(uri), []string{"baseline_default_padding", "baseline_unmuxed", "maestro_advanced_not_claimed"}})
	if err != nil {
		return Artifacts{}, invalid("receipt_encode")
	}
	return Artifacts{append([]byte(nil), server...), append([]byte(nil), direct...), append([]byte(nil), cdn...), append([]byte(nil), uri...), append([]byte(nil), receipt...)}, nil
}
func sha(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func xhttp(s Snapshot) map[string]any {
	return map[string]any{"host": s.Request.PublicHost, "path": s.Material.SecretPath, "mode": "packet-up", "uplinkHTTPMethod": "GET", "uplinkDataPlacement": "body", "sessionIDPlacement": "query", "sessionIDKey": "auth", "sessionIDLength": 16, "seqPlacement": "query", "seqKey": "chunk_id"}
}
func stream(s Snapshot, security string) map[string]any {
	result := map[string]any{"network": "xhttp", "security": security, "xhttpSettings": xhttp(s)}
	if security == "tls" {
		result["tlsSettings"] = map[string]any{"serverName": s.Request.PublicHost}
	}
	return result
}
func serverConfig(s Snapshot) map[string]any {
	return map[string]any{"log": map[string]any{"access": "none", "error": "none", "loglevel": "warning"}, "api": map[string]any{"tag": "api", "services": []string{"StatsService"}}, "stats": map[string]any{}, "policy": map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true}}}, "inbounds": []any{map[string]any{"listen": "0.0.0.0", "port": 18081, "protocol": "vless", "tag": "canary-in", "settings": map[string]any{"clients": []any{map[string]any{"id": s.Material.ClientID, "email": s.Material.ClientEmail, "level": 0}}, "decryption": s.Material.ServerDecryption}, "streamSettings": stream(s, "none")}, map[string]any{"listen": "127.0.0.1", "port": 18082, "protocol": "dokodemo-door", "tag": "api", "settings": map[string]any{"address": "127.0.0.1"}}}, "outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}}, "routing": map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api"}}}}
}
func clientConfig(s Snapshot, address string, port int, security string, socksPort int) map[string]any {
	return map[string]any{"log": map[string]any{"access": "none", "error": "none", "loglevel": "warning"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": socksPort, "protocol": "socks", "settings": map[string]any{"auth": "noauth", "udp": false}}}, "outbounds": []any{map[string]any{"protocol": "vless", "tag": "proxy", "settings": map[string]any{"vnext": []any{map[string]any{"address": address, "port": port, "users": []any{map[string]any{"id": s.Material.ClientID, "encryption": s.Material.ClientEncryption}}}}}, "streamSettings": stream(s, security)}}}
}
func clientURI(s Snapshot) string {
	extra, _ := json.Marshal(map[string]any{"sessionIDPlacement": "query", "sessionIDKey": "auth", "sessionIDLength": 16, "seqPlacement": "query", "seqKey": "chunk_id", "uplinkHTTPMethod": "GET", "uplinkDataPlacement": "body"})
	values := url.Values{"encryption": []string{s.Material.ClientEncryption}, "security": []string{"tls"}, "type": []string{"xhttp"}, "host": []string{s.Request.PublicHost}, "sni": []string{s.Request.PublicHost}, "path": []string{s.Material.SecretPath}, "mode": []string{"packet-up"}, "extra": []string{string(extra)}}
	return "vless://" + s.Material.ClientID + "@" + s.Request.PublicHost + ":443?" + values.Encode() + "#maestro-xhttp-canary"
}
