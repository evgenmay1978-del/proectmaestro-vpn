package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
)

const wbRoomProviderURL = "https://stream.wb.ru/api-room/api/v2/room"
const wbRoomResponseLimit = 4096

type settingSecretReader interface {
	ReadSettingSecret(context.Context, string) ([]byte, error)
}

type WBRoomSender struct {
	secrets settingSecretReader
	client  *http.Client
}

func NewWBRoomSender(secrets settingSecretReader, client *http.Client) (*WBRoomSender, error) {
	if secrets == nil || client == nil {
		return nil, errors.New("api: WB room provider is unavailable")
	}
	return &WBRoomSender{secrets: secrets, client: client}, nil
}

func (s *WBRoomSender) Post(ctx context.Context, request []byte) ([]byte, error) {
	var input struct { Login string `json:"login"` }
	if s == nil || s.secrets == nil || s.client == nil || json.Unmarshal(request, &input) != nil || strings.TrimSpace(input.Login) == "" {
		return nil, errors.New("api: invalid WB room request")
	}
	token, err := s.secrets.ReadSettingSecret(ctx, "wbstream")
	if err != nil || strings.TrimSpace(string(token)) == "" {
		return nil, errors.New("api: WB room token is unavailable")
	}
	payload, _ := json.Marshal(map[string]string{
		"roomType": "ROOM_TYPE_ALL_ON_SCREEN", "roomPrivacy": "ROOM_PRIVACY_FREE",
	})
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, wbRoomProviderURL, bytes.NewReader(payload))
	if err != nil { return nil, errors.New("api: build WB room request") }
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "MaestroVPN-ControlPlane/1")
	response, err := s.client.Do(httpRequest)
	if err != nil { return nil, errors.New("api: WB room provider unavailable") }
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, wbRoomResponseLimit+1))
	if err != nil || len(body) > wbRoomResponseLimit || response.StatusCode != http.StatusOK {
		return nil, errors.New("api: WB room provider rejected request")
	}
	var provider struct { RoomID string `json:"roomId"` }
	if json.Unmarshal(body, &provider) != nil || strings.TrimSpace(provider.RoomID) == "" {
		return nil, errors.New("api: WB room provider response is invalid")
	}
	return json.Marshal(map[string]string{"room": strings.TrimSpace(provider.RoomID)})
}

var _ controlplane.ExternalActionSender = (*WBRoomSender)(nil)
