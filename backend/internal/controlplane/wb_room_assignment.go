package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// AssignWBRoom records a successful provider response through the canonical
// OLCRTC setting transaction, including desired state and its outbox event.
func (s *Service) AssignWBRoom(ctx context.Context, login, room, idempotencyKey string) error {
	login = strings.TrimSpace(login)
	room = strings.TrimSpace(room)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || login == "" || room == "" || idempotencyKey == "" {
		return errors.New("controlplane: invalid WB room assignment")
	}

	setting, err := s.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	members := make([]string, 0, len(setting.Members)+1)
	for member := range setting.Members {
		members = append(members, member)
	}
	sort.Strings(members)
	if !hasWBRoomMember(members, login) {
		members = append(members, login)
		sort.Strings(members)
	}

	value, err := json.Marshal(map[string]string{"room": room, "provider": "wbstream"})
	if err != nil {
		return errors.New("controlplane: encode WB room assignment")
	}
	_, alreadyMember := setting.Members[login]
	if string(setting.PublicValueJSON) == string(value) && alreadyMember {
		return nil
	}
	_, err = s.UpdateSetting(ctx, SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: setting.Generation, PublicValueJSON: string(value),
		Members: members, Actor: "panel", CommandType: "setting.olcrtc.wbroom",
		IdempotencyKey: idempotencyKey, TargetMembers: append([]string(nil), members...),
	})
	return err
}

func hasWBRoomMember(members []string, want string) bool {
	for _, member := range members {
		if member == want {
			return true
		}
	}
	return false
}
