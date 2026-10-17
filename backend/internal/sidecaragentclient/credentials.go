package sidecaragentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const CredentialPath = "/v1/credentials"

var credentialEmail = regexp.MustCompile(`^wl:[A-Za-z0-9_.-]{1,200}:exit-s[1-4]$`)
var credentialUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// WhiteListCredentialTemplate reads only the operator-provided client half of
// the commercial transport. No server decryption material is derived or read.
func (client *Client) WhiteListCredentialTemplate(ctx context.Context) ([]byte, error) {
	if client == nil || ctx == nil || ctx.Err() != nil || client.credentialTemplateFile == "" {
		return nil, ErrInvalidRequest
	}
	info, err := os.Lstat(client.credentialTemplateFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() < 1 || info.Size() > 16384 {
		return nil, ErrInvalidRequest
	}
	file, err := os.Open(client.credentialTemplateFile)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrInvalidRequest
	}
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	after, statErr := os.Lstat(client.credentialTemplateFile)
	if err != nil || statErr != nil || len(raw) != int(info.Size()) || len(raw) > 16384 ||
		!os.SameFile(info, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0077 != 0 ||
		!info.ModTime().Equal(after.ModTime()) || info.Size() != after.Size() {
		return nil, ErrInvalidRequest
	}
	return raw, nil
}

// InstallCredential is replay-safe because the agent accepts only the already
// stored UUID for an existing identity. A lost response can be retried verbatim.
func (client *Client) InstallCredential(ctx context.Context, releaseID, configDigest, email, clientID string) error {
	if client == nil || client.httpClient == nil || ctx == nil || client.requestTimeout <= 0 ||
		releaseID == "" || len(releaseID) > 256 || strings.TrimSpace(releaseID) != releaseID ||
		strings.ContainsAny(releaseID, "\x00\r\n\t") || !validLeaseDigest(configDigest) ||
		!credentialEmail.MatchString(email) || !credentialUUID.MatchString(clientID) {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(struct {
		Schema       int    `json:"schema"`
		ReleaseID    string `json:"release_id"`
		ConfigDigest string `json:"config_digest"`
		ManagedEmail string `json:"managed_email"`
		ClientID     string `json:"client_id"`
	}{1, releaseID, configDigest, email, clientID})
	if err != nil {
		return ErrInvalidRequest
	}
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, client.baseURL+CredentialPath, bytes.NewReader(body))
	if err != nil {
		return ErrInvalidRequest
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cache-Control", "no-store")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ErrDeliveryUnknown
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	return ErrRequestRejected
}
