package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/rqlite"
)

func (s *Service) ReadSettingSecret(ctx context.Context, key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if s == nil || s.store == nil || key == "" {
		return nil, ErrUnavailable
	}
	results, err := s.store.db.QueryLinearizable(ctx, rqlite.Statement{
		SQL: `SELECT secret_envelope FROM setting_secrets WHERE setting_key=?`, Args: []any{key},
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	row, ok := firstRow(results)
	if !ok {
		return nil, ErrNotFound
	}
	encoded, ok := rowString(row, "secret_envelope")
	if !ok {
		return nil, ErrUnavailable
	}
	envelopeJSON, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrUnavailable
	}
	var envelope Envelope
	if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
		return nil, ErrUnavailable
	}
	plaintext, err := s.store.secrets.Open(SecretScope{
		OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key,
	}, envelope)
	if err != nil {
		return nil, ErrUnavailable
	}
	return plaintext, nil
}
