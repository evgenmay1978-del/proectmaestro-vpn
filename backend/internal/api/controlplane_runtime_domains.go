package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/olcconf"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/vkturnconf"
)

type runtimeSettingSource interface {
	ReadLegacyRuntimeSetting(context.Context, string) (controlplane.LegacyRuntimeSetting, error)
}

type subscriptionRuntimeIdentity struct {
	OLCGeneration int64
	VKGeneration  int64
	Digest        string
}

type subscriptionRuntimeProjection struct {
	Identity subscriptionRuntimeIdentity
	OLC      *subgen.OLCRTCCreds
	VKTurn   *subgen.VKTurnCreds
	Info     json.RawMessage
}

func decodeRuntimeConfig(raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 1<<20 || decoder.Decode(into) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return controlplane.ErrUnavailable
	}
	return nil
}

// Runtime credentials are resolved for this exact account, never inherited from
// the shared subscription topology. The controlplane reader authenticates both
// the encrypted configuration and its current customer/member bindings.
func (b *ServiceBusiness) subscriptionRuntime(ctx context.Context, customer CustomerView, options subscriptionRenderOptions, settingsGeneration int64) (subscriptionRuntimeProjection, error) {
	var projection subscriptionRuntimeProjection
	if b.runtimeSettings == nil || !customer.Active || options.endpoint() == subscriptionEndpointHelpers {
		return projection, nil
	}
	info := map[string]any{}
	digest := sha256.New()
	for _, key := range []string{"olcrtc", "vkturn"} {
		value, err := b.runtimeSettings.ReadLegacyRuntimeSetting(ctx, key)
		if errors.Is(err, controlplane.ErrNotFound) {
			continue
		}
		if err != nil {
			return subscriptionRuntimeProjection{}, err
		}
		generation := value.Generation()
		if generation < 1 || (settingsGeneration >= 0 && generation > settingsGeneration) {
			return subscriptionRuntimeProjection{}, controlplane.ErrUnavailable
		}
		memberAllowed := false
		for _, member := range value.Members() {
			if member.Login == customer.Login {
				if member.CustomerID != customer.CustomerID {
					return subscriptionRuntimeProjection{}, controlplane.ErrUnavailable
				}
				memberAllowed = true
			}
		}
		raw := value.ConfigJSON()
		_, _ = digest.Write([]byte(key + "\x00"))
		_, _ = digest.Write(raw)
		_, _ = digest.Write([]byte{0})
		if key == "olcrtc" {
			projection.Identity.OLCGeneration = generation
			var config olcconf.Config
			if decodeRuntimeConfig(raw, &config) != nil {
				return subscriptionRuntimeProjection{}, controlplane.ErrUnavailable
			}
			if memberAllowed && config.Enabled && config.Allowed(customer.Login) && config.Dedicated(customer.Login) {
				if room, secret, ok := config.RoomFor(customer.Login); ok {
					projection.OLC = &subgen.OLCRTCCreds{Provider: config.ProviderFor(customer.Login), Room: room, Key: secret, Transport: config.Transport}
					info["olcrtc"] = map[string]any{"provider": config.ProviderFor(customer.Login), "room": room, "key": secret, "transport": config.Transport}
				}
			}
		} else {
			projection.Identity.VKGeneration = generation
			var config vkturnconf.Config
			if decodeRuntimeConfig(raw, &config) != nil || config.Validate() != nil {
				return subscriptionRuntimeProjection{}, controlplane.ErrUnavailable
			}
			if memberAllowed && config.Enabled && strings.EqualFold(strings.TrimSpace(options.Platform), "mobile") && appVersionCode(options.UserAgent) >= config.MinVersionCode {
				if client, ok := config.ClientFor(customer.Login); ok {
					wireguard := client.WG
					projection.VKTurn = &wireguard
					info["features"] = map[string]any{"vk_turn": true}
					info["vk_turn"] = map[string]any{"server": config.Server, "vk_hashes": config.VKHashes, "password": client.Password,
						"workers": vkturnconf.DefaultWorkers, "fingerprint": vkturnconf.DefaultFingerprint,
						"client_ids": append([]string(nil), vkturnconf.DefaultClientIDs...), "obfs_mode": vkturnconf.DefaultObfsMode}
				}
			}
		}
	}
	if projection.Identity.OLCGeneration != 0 || projection.Identity.VKGeneration != 0 {
		projection.Identity.Digest = hex.EncodeToString(digest.Sum(nil))
	}
	if len(info) != 0 {
		projection.Info, _ = json.Marshal(info)
	}
	return projection, nil
}

func runtimeOLCView(value controlplane.LegacyRuntimeSetting) (OLCRTCView, error) {
	var config olcconf.Config
	if decodeRuntimeConfig(value.ConfigJSON(), &config) != nil {
		return OLCRTCView{}, controlplane.ErrUnavailable
	}
	view := OLCRTCView{Room: config.Room, Provider: config.Provider, Logins: append([]string(nil), config.Logins...), Rooms: map[string]OLCRTCRoomView{}}
	for _, login := range config.Logins {
		room, _, _ := config.RoomFor(login)
		view.Rooms[login] = OLCRTCRoomView{Room: room, Provider: config.ProviderFor(login)}
	}
	return view, nil
}

func runtimeVKView(value controlplane.LegacyRuntimeSetting) (VKTurnView, error) {
	var config vkturnconf.Config
	if decodeRuntimeConfig(value.ConfigJSON(), &config) != nil || config.Validate() != nil {
		return VKTurnView{}, controlplane.ErrUnavailable
	}
	return VKTurnView{Enabled: config.Enabled, Server: config.Server}, nil
}
