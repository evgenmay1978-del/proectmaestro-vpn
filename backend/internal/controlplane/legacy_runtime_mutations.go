package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
)

// The original schema1 document stays in imported_secrets. Mutable schema2
// binds that authenticated source and the current setting generation explicitly.
type legacyRuntimeCurrentDocument struct {
	SchemaVersion int                          `json:"schema_version"`
	SourceSHA256  string                       `json:"source_sha256"`
	Generation    int64                        `json:"generation"`
	Current       LegacyRuntimeSettingDocument `json:"current"`
}

type legacyRuntimeSourceSecret struct {
	SecretID       string `json:"secret_id"`
	OwnerType      string `json:"owner_type"`
	OwnerSourceKey string `json:"owner_source_key"`
	Field          string `json:"field"`
	Kind           string `json:"kind"`
	KeyVersion     int    `json:"key_version"`
	NonceB64       string `json:"nonce_b64"`
	CiphertextB64  string `json:"ciphertext_b64"`
	SHA256         string `json:"sha256"`
}

func authenticateRuntimeSource(box *SecretBox, key string, row map[string]any) (LegacyRuntimeSetting, error) {
	id, idOK := rowString(row, "secret_id")
	digest, dOK := rowString(row, "secret_sha256")
	raw, rOK := rowString(row, "source_envelope")
	envelopeSHA, hOK := rowString(row, "source_envelope_sha256")
	version, vOK := rowInt64(row, "key_version")
	lifecycle, lOK := rowString(row, "lifecycle")
	if !idOK || !dOK || !rOK || !hOK || !vOK || !lOK || lifecycle != "active" || version < 1 || len(raw) > 2<<20 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	sum := sha256.Sum256([]byte(raw))
	var secret legacyRuntimeSourceSecret
	if hex.EncodeToString(sum[:]) != envelopeSHA || decodeRuntimeDomain([]byte(raw), &secret) != nil || secret.SecretID != id || secret.OwnerType != "setting" || secret.OwnerSourceKey != key || secret.Field != "secret" || secret.Kind != key || int64(secret.KeyVersion) != version || secret.SHA256 != digest {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	nonce, err := base64.StdEncoding.Strict().DecodeString(secret.NonceB64)
	if err != nil || base64.StdEncoding.EncodeToString(nonce) != secret.NonceB64 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	ciphertext, err := base64.StdEncoding.Strict().DecodeString(secret.CiphertextB64)
	if err != nil || base64.StdEncoding.EncodeToString(ciphertext) != secret.CiphertextB64 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	envelopeJSON, _ := json.Marshal(Envelope{KeyVersion: secret.KeyVersion, Nonce: nonce, Ciphertext: ciphertext})
	value, err := AuthenticateLegacyRuntimeSetting(box, key, base64.StdEncoding.EncodeToString(envelopeJSON), digest)
	if err != nil || id != "runtime-setting-v1:"+key+":"+value.document.CapsuleSHA256 {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	return value, nil
}

func authenticateRuntimeCurrent(box *SecretBox, key, encoded, digest string, generation int64, source LegacyRuntimeSetting) (LegacyRuntimeSetting, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	var envelope Envelope
	if err != nil || len(raw) > 2<<20 || base64.StdEncoding.EncodeToString(raw) != encoded || decodeRuntimeDomain(raw, &envelope) != nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	plain, err := box.Open(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, envelope)
	if err != nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	var marker struct {
		SchemaVersion int `json:"schema_version"`
	}
	if json.Unmarshal(plain, &marker) != nil {
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	var value LegacyRuntimeSetting
	switch marker.SchemaVersion {
	case 1:
		value, err = validateLegacyRuntimeSettingPlain(box, key, plain)
		if err != nil || value.digest != digest || value.digest != source.digest {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
	case 2:
		var current legacyRuntimeCurrentDocument
		sum := sha256.Sum256(raw)
		if decodeRuntimeDomain(plain, &current) != nil || current.SourceSHA256 != source.digest || current.Generation != generation || generation < 2 || hex.EncodeToString(sum[:]) != digest {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
		currentJSON, _ := json.Marshal(current.Current)
		value, err = validateLegacyRuntimeSettingPlain(box, key, currentJSON)
		if err != nil || value.document.CapsuleSHA256 != source.document.CapsuleSHA256 || value.document.CustomersSHA256 != source.document.CustomersSHA256 {
			return LegacyRuntimeSetting{}, ErrUnavailable
		}
	default:
		return LegacyRuntimeSetting{}, ErrUnavailable
	}
	value.source, value.sourceSHA, value.generation = source.document, source.digest, generation
	return value, nil
}

type LegacyRuntimeSettingUpdate struct {
	Current            LegacyRuntimeSetting
	ExpectedGeneration int64
	CommandType        string
	IdempotencyKey     string
	Actor              string
	RequestJSON        json.RawMessage
	ConfigJSON         json.RawMessage
	PublicValueJSON    json.RawMessage
	TargetPayloads     map[string]string
}

// UpdateLegacyRuntimeSetting uses the existing canonical setting transaction.
// RequestJSON is the original command, so retries do not hash a newly resealed
// ciphertext or a later read of the current configuration.
func (s *Service) UpdateLegacyRuntimeSetting(ctx context.Context, command LegacyRuntimeSettingUpdate) (SettingResult, error) {
	if s == nil || s.store == nil || command.Current.sourceSHA == "" || command.Current.generation < 1 || strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.Actor) == "" {
		return SettingResult{}, ErrForbidden
	}
	key := command.Current.document.SettingKey
	allowed := (key == "olcrtc" && (command.CommandType == "setting.olcrtc.room" || command.CommandType == "setting.olcrtc.grant" || command.CommandType == "setting.olcrtc.wbroom")) || (key == "vkturn" && (command.CommandType == "setting.vkturn.update" || command.CommandType == "setting.vkturn.enabled"))
	if !allowed || command.ExpectedGeneration < 0 && command.CommandType != "setting.olcrtc.wbroom" {
		return SettingResult{}, ErrForbidden
	}
	var original any
	if decodeRuntimeDomain(command.RequestJSON, &original) != nil {
		return SettingResult{}, ErrForbidden
	}
	requestJSON, err := json.Marshal(struct {
		Key, CommandType, SourceSHA string
		ExpectedGeneration          int64
		Request                     any
	}{key, command.CommandType, command.Current.sourceSHA, command.ExpectedGeneration, original})
	if err != nil {
		return SettingResult{}, ErrForbidden
	}
	requestHash := s.store.secrets.LookupHMAC("legacy-runtime-setting-command-v1", requestJSON)
	update := SettingUpdate{Key: key, CommandType: command.CommandType, IdempotencyKey: command.IdempotencyKey, Actor: command.Actor}
	if replay, found, err := s.resolveSettingMutation(ctx, update, requestHash); found || err != nil {
		return replay, err
	}
	expected := command.ExpectedGeneration
	// Existing panel requests may omit a version. They still compare-and-swap
	// the authenticated generation just read; the original omission stays in the
	// idempotency hash so a retry never turns into a new command.
	if expected == 0 || (command.CommandType == "setting.olcrtc.wbroom" && expected < 0) {
		expected = command.Current.generation
	}
	if expected != command.Current.generation || expected == math.MaxInt64 {
		return SettingResult{}, ErrConflict
	}
	var config map[string]json.RawMessage
	if decodeRuntimeDomain(command.ConfigJSON, &config) != nil || config == nil || !json.Valid(command.PublicValueJSON) {
		return SettingResult{}, ErrForbidden
	}
	var logins []string
	if key == "olcrtc" {
		if decodeRuntimeDomain(config["logins"], &logins) != nil {
			return SettingResult{}, ErrForbidden
		}
	} else {
		var clients map[string]json.RawMessage
		if decodeRuntimeDomain(config["clients"], &clients) != nil || clients == nil {
			return SettingResult{}, ErrForbidden
		}
		for login := range clients {
			logins = append(logins, login)
		}
	}
	sort.Strings(logins)
	doc := command.Current.document
	doc.ConfigJSON = append(json.RawMessage(nil), command.ConfigJSON...)
	doc.Members = []LegacyRuntimeMember{}
	for index, login := range logins {
		if index > 0 && logins[index-1] == login {
			return SettingResult{}, ErrConflict
		}
		identity, err := s.ResolveCustomerLogin(ctx, login)
		if err != nil {
			return SettingResult{}, err
		}
		if !identity.Exists() || !identity.ExactLegacy() {
			return SettingResult{}, ErrConflict
		}
		doc.Members = append(doc.Members, LegacyRuntimeMember{Login: identity.Login(), CustomerSourceKey: identity.source, CustomerID: identity.CustomerID(), CustomerSHA256: identity.digest, LoginHMAC: identity.LookupHMAC(), MemberHMAC: identity.SettingMemberHMAC(key)})
	}
	plainDoc, _ := json.Marshal(doc)
	if _, err := validateLegacyRuntimeSettingPlain(s.store.secrets, key, plainDoc); err != nil {
		return SettingResult{}, ErrForbidden
	}
	plain, err := json.Marshal(legacyRuntimeCurrentDocument{SchemaVersion: 2, SourceSHA256: command.Current.sourceSHA, Generation: expected + 1, Current: doc})
	if err != nil {
		return SettingResult{}, ErrUnavailable
	}
	defer wipeDesiredPayloadBytes(plain)
	envelope, err := s.store.secrets.Seal(SecretScope{OwnerType: "setting", OwnerID: key, Field: "secret", Kind: key}, plain)
	if err != nil {
		return SettingResult{}, ErrUnavailable
	}
	update.ExpectedGeneration, update.PublicValueJSON, update.Secret, update.Members = expected, string(command.PublicValueJSON), &envelope, logins
	update.TargetPayloads = command.TargetPayloads
	for login := range command.TargetPayloads {
		update.TargetMembers = append(update.TargetMembers, login)
	}
	sort.Strings(update.TargetMembers)
	if !validSettingUpdate(update) {
		return SettingResult{}, ErrForbidden
	}
	mutationToken, err := s.ids.NewID("setting-mut")
	if err != nil {
		return SettingResult{}, ErrUnavailable
	}
	return s.store.updateSettingIdempotent(ctx, update, mutationToken, requestHash)
}

var runtimeWBRoomID = regexp.MustCompile(`^[A-Za-z0-9._~-]{8,128}$`)

func validRuntimeOLCRoom(room, provider string) bool {
	if provider == "wbstream" {
		return runtimeWBRoomID.MatchString(room)
	}
	parsed, err := url.Parse(room)
	return provider == "telemost" && err == nil && parsed.Scheme == "https" && parsed.Host == "telemost.yandex.ru" && strings.HasPrefix(parsed.Path, "/j/") && len(parsed.Path) > 3 && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func legacyRuntimeOLCPublic(config olcconf.Config) json.RawMessage {
	rooms := map[string]map[string]string{}
	for login := range config.Rooms {
		room, _, _ := config.RoomFor(login)
		rooms[login] = map[string]string{"room": room, "provider": config.ProviderFor(login)}
	}
	raw, _ := json.Marshal(map[string]any{"enabled": config.Enabled, "provider": config.Provider, "transport": config.Transport, "rooms": rooms})
	return raw
}

// Existing room edits preserve the customer's established transport key.
// Creating a new key/server instance is outside this command's authority.
func (s *Service) SetLegacyOLCRoom(ctx context.Context, current LegacyRuntimeSetting, login, room, provider string, expected int64, idempotencyKey, commandType string) (SettingResult, json.RawMessage, error) {
	var config olcconf.Config
	if current.document.SettingKey != "olcrtc" || decodeRuntimeDomain(current.ConfigJSON(), &config) != nil {
		return SettingResult{}, nil, ErrUnavailable
	}
	identity, err := s.ResolveCustomerLogin(ctx, login)
	if err != nil {
		return SettingResult{}, nil, err
	}
	login = identity.Login()
	room = strings.TrimSpace(room)
	provider = strings.TrimSpace(provider)
	requestedProvider := provider
	if provider == "" {
		provider = config.ProviderFor(login)
	}
	existing, ok := config.Rooms[login]
	if !identity.Exists() || !ok || existing.Key == "" || !validRuntimeOLCRoom(room, provider) {
		return SettingResult{}, nil, ErrConflict
	}
	request, _ := json.Marshal(struct{ Login, Room, Provider string }{login, room, requestedProvider})
	existing.Room, existing.Provider = room, provider
	config.Rooms[login] = existing
	config.Enabled = true
	if !config.Allowed(login) {
		config.Logins = append(config.Logins, login)
		sort.Strings(config.Logins)
	}
	next, _ := json.Marshal(config)
	public := legacyRuntimeOLCPublic(config)
	target, _ := json.Marshal(map[string]any{"room": room, "provider": provider, "enabled": true})
	result, err := s.UpdateLegacyRuntimeSetting(ctx, LegacyRuntimeSettingUpdate{Current: current, ExpectedGeneration: expected, CommandType: commandType, IdempotencyKey: idempotencyKey, Actor: "panel", RequestJSON: request, ConfigJSON: next, PublicValueJSON: public, TargetPayloads: map[string]string{login: string(target)}})
	return result, public, err
}

func (s *Service) SetLegacyOLCGrant(ctx context.Context, current LegacyRuntimeSetting, login string, enabled bool, expected int64, idempotencyKey string) (SettingResult, json.RawMessage, error) {
	var config olcconf.Config
	if current.document.SettingKey != "olcrtc" || decodeRuntimeDomain(current.ConfigJSON(), &config) != nil {
		return SettingResult{}, nil, ErrUnavailable
	}
	identity, err := s.ResolveCustomerLogin(ctx, login)
	if err != nil {
		return SettingResult{}, nil, err
	}
	login = identity.Login()
	if !identity.Exists() || enabled && !config.Dedicated(login) {
		return SettingResult{}, nil, ErrConflict
	}
	request, _ := json.Marshal(struct {
		Login   string
		Enabled bool
	}{login, enabled})
	logins := []string{}
	for _, member := range config.Logins {
		if member != login {
			logins = append(logins, member)
		}
	}
	if enabled {
		logins = append(logins, login)
	}
	sort.Strings(logins)
	config.Logins = logins
	next, _ := json.Marshal(config)
	public := legacyRuntimeOLCPublic(config)
	room, _, _ := config.RoomFor(login)
	target, _ := json.Marshal(map[string]any{"room": room, "provider": config.ProviderFor(login), "enabled": enabled})
	result, err := s.UpdateLegacyRuntimeSetting(ctx, LegacyRuntimeSettingUpdate{Current: current, ExpectedGeneration: expected, CommandType: "setting.olcrtc.grant", IdempotencyKey: idempotencyKey, Actor: "panel", RequestJSON: request, ConfigJSON: next, PublicValueJSON: public, TargetPayloads: map[string]string{login: string(target)}})
	return result, public, err
}

func (s *Service) assignLegacyWBRoom(ctx context.Context, login, room, idempotencyKey string) (bool, error) {
	current, err := s.ReadLegacyRuntimeSetting(ctx, "olcrtc")
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	_, _, err = s.SetLegacyOLCRoom(ctx, current, login, room, "wbstream", -1, idempotencyKey, "setting.olcrtc.wbroom")
	return true, err
}
