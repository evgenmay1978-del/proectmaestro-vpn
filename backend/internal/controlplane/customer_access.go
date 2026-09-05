package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

var canonicalCustomerProtocols = []string{"anytls", "hysteria2", "naive", "vless"}

type CustomerAccess struct {
	SubscriptionToken   string            `json:"subscription_token"`
	Credentials         map[string]string `json:"credentials"`
	CredentialUsernames map[string]string `json:"credential_usernames,omitempty"`
}

type sealedCustomerSecret struct {
	ID       string
	Kind     string
	Raw      string
	Envelope []byte
	Digest   string
}

type customerAccessMint struct {
	Access      CustomerAccess
	Token       sealedCustomerSecret
	Credentials []sealedCustomerSecret
}

func (s *Service) mintCustomerAccess(customerID string) (customerAccessMint, error) {
	access := customerAccessMint{Access: CustomerAccess{Credentials: make(map[string]string)}}
	rawToken, err := s.ids.NewID("subscription")
	if err != nil {
		return customerAccessMint{}, errors.New("controlplane: generate subscription token")
	}
	tokenID, err := s.ids.NewID("subscription_token")
	if err != nil {
		return customerAccessMint{}, errors.New("controlplane: generate subscription token identifier")
	}
	access.Token, err = s.sealCustomerSecret(customerID, tokenID, "token", "subscription", rawToken)
	if err != nil {
		return customerAccessMint{}, err
	}
	access.Access.SubscriptionToken = rawToken
	for _, protocol := range canonicalCustomerProtocols {
		raw, idErr := s.ids.NewID("credential_" + protocol)
		if idErr != nil {
			return customerAccessMint{}, errors.New("controlplane: generate protocol credential")
		}
		credentialID, idErr := s.ids.NewID("credential")
		if idErr != nil {
			return customerAccessMint{}, errors.New("controlplane: generate credential identifier")
		}
		sealed, sealErr := s.sealCustomerSecret(customerID, credentialID, "credential", protocol, raw)
		if sealErr != nil {
			return customerAccessMint{}, sealErr
		}
		access.Credentials = append(access.Credentials, sealed)
		access.Access.Credentials[protocol] = raw
	}
	return access, nil
}

func (s *Service) sealCustomerSecret(customerID, id, field, kind, raw string) (sealedCustomerSecret, error) {
	envelope, err := s.store.secrets.Seal(SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: field, Kind: kind,
	}, []byte(raw))
	if err != nil {
		return sealedCustomerSecret{}, err
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return sealedCustomerSecret{}, errors.New("controlplane: encode customer access envelope")
	}
	digest := sha256.Sum256([]byte(raw))
	return sealedCustomerSecret{ID: id, Kind: kind, Raw: raw, Envelope: envelopeBytes, Digest: hex.EncodeToString(digest[:])}, nil
}

func (m customerAccessMint) statements(customer Customer, now int64, guard string, guardArgs []any, secrets *SecretBox) []rqlite.Statement {
	if m.Access.SubscriptionToken == "" {
		return nil
	}
	statements := []rqlite.Statement{{
		SQL: `INSERT INTO subscription_tokens(token_id,customer_id,token_hmac,token_envelope,token_sha256,generation,revoked,created_at_unix)
SELECT ?,?,?,?,?,?,0,? WHERE ` + guard,
		Args: append([]any{
			m.Token.ID, customer.ID, secrets.LookupHMAC("subscription-token", []byte(m.Token.Raw)),
			m.Token.Envelope, m.Token.Digest, customer.Generation, now,
		}, guardArgs...),
	}}
	for _, credential := range m.Credentials {
		statements = append(statements, rqlite.Statement{
			SQL: `INSERT INTO credentials(credential_id,customer_id,protocol,secret_envelope,secret_sha256,generation,enabled,created_at_unix,updated_at_unix)
SELECT ?,?,?,?,?,?,1,?,? WHERE ` + guard,
			Args: append([]any{
				credential.ID, customer.ID, credential.Kind, credential.Envelope, credential.Digest,
				customer.Generation, now, now,
			}, guardArgs...),
		})
	}
	return statements
}

func (s *Service) customerAccess(ctx context.Context, customerID string) (CustomerAccess, error) {
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT st.token_envelope, cr.protocol, cr.secret_envelope
FROM subscription_tokens st JOIN credentials cr ON cr.customer_id=st.customer_id
WHERE st.customer_id=? AND st.revoked=0 AND cr.enabled=1
AND cr.generation=(SELECT max(c2.generation) FROM credentials c2 WHERE c2.customer_id=st.customer_id AND c2.protocol=cr.protocol)
ORDER BY cr.protocol`,
		Args: []any{customerID},
	})
	if err != nil || len(results) != 1 || len(results[0].Rows) == 0 {
		return CustomerAccess{}, ErrUnavailable
	}
	access := CustomerAccess{Credentials: make(map[string]string)}
	for _, row := range results[0].Rows {
		protocol, ok := rowString(row, "protocol")
		if !ok {
			return CustomerAccess{}, ErrUnavailable
		}
		if access.SubscriptionToken == "" {
			raw, openErr := s.openCustomerSecret(row, "token_envelope", customerID, "token", "subscription")
			if openErr != nil {
				return CustomerAccess{}, openErr
			}
			access.SubscriptionToken = raw
		}
		raw, username, openErr := s.openCustomerCredential(row, customerID, protocol)
		if openErr != nil {
			return CustomerAccess{}, openErr
		}
		access.Credentials[protocol] = raw
		if username != "" {
			if access.CredentialUsernames == nil {
				access.CredentialUsernames = make(map[string]string)
			}
			access.CredentialUsernames[protocol] = username
		}
	}
	if access.SubscriptionToken == "" || len(access.Credentials) == 0 {
		return CustomerAccess{}, ErrUnavailable
	}
	return access, nil
}

func (s *Service) openCustomerSecret(row map[string]any, column, customerID, field, kind string) (string, error) {
	encoded, ok := rowString(row, column)
	if !ok {
		return "", ErrUnavailable
	}
	bytes, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrUnavailable
	}
	var envelope Envelope
	if err := json.Unmarshal(bytes, &envelope); err != nil {
		return "", ErrUnavailable
	}
	plaintext, err := s.store.secrets.Open(SecretScope{
		OwnerType: "customer", OwnerID: customerID, Field: field, Kind: kind,
	}, envelope)
	if err != nil {
		return "", ErrUnavailable
	}
	return string(plaintext), nil
}

func accessPayload(access CustomerAccess) map[string]any {
	if access.SubscriptionToken == "" {
		return nil
	}
	protocols := make([]string, 0, len(access.Credentials))
	for protocol := range access.Credentials {
		protocols = append(protocols, protocol)
	}
	sort.Strings(protocols)
	payload := map[string]any{
		"subscription_token": access.SubscriptionToken,
		"credentials":        access.Credentials,
		"protocols":          protocols,
	}
	if len(access.CredentialUsernames) != 0 {
		payload["credential_usernames"] = access.CredentialUsernames
	}
	return payload
}
