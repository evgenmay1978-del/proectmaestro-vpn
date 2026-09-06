package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type olcrtcRoomAssignment struct {
	Room     string `json:"room"`
	Provider string `json:"provider"`
}

type olcrtcRoomAssignments struct {
	Rooms map[string]olcrtcRoomAssignment `json:"rooms"`
}

func (s *Service) decodeWBRoomAssignments(ctx context.Context, setting BusinessSetting, target CustomerLoginIdentity) (olcrtcRoomAssignments, error) {
	state := olcrtcRoomAssignments{Rooms: map[string]olcrtcRoomAssignment{}}
	if len(setting.PublicValueJSON) == 0 {
		return state, nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(setting.PublicValueJSON, &document); err != nil {
		return olcrtcRoomAssignments{}, ErrUnavailable
	}
	if rawRooms, ok := document["rooms"]; ok {
		var persisted map[string]olcrtcRoomAssignment
		if err := json.Unmarshal(rawRooms, &persisted); err != nil {
			return olcrtcRoomAssignments{}, ErrUnavailable
		}
		keys := make([]string, 0, len(persisted))
		for login := range persisted {
			keys = append(keys, login)
		}
		sort.Strings(keys)
		matchedMembers := 0
		for _, login := range keys {
			room := persisted[login]
			identity, err := s.ResolveCustomerLogin(ctx, login)
			if err != nil {
				return olcrtcRoomAssignments{}, err
			}
			room.Room = strings.TrimSpace(room.Room)
			room.Provider = strings.TrimSpace(room.Provider)
			if room.Room == "" || room.Provider == "" {
				return olcrtcRoomAssignments{}, ErrUnavailable
			}
			if _, duplicate := state.Rooms[identity.Login()]; duplicate {
				return olcrtcRoomAssignments{}, ErrConflict
			}
			if value, member := setting.Members[identity.SettingMemberHMAC("olcrtc")]; member {
				var membership struct {
					Enabled bool `json:"enabled"`
				}
				if json.Unmarshal(value, &membership) != nil || !membership.Enabled {
					return olcrtcRoomAssignments{}, ErrConflict
				}
				matchedMembers++
			}
			state.Rooms[identity.Login()] = room
		}
		if matchedMembers != len(setting.Members) {
			return olcrtcRoomAssignments{}, ErrConflict
		}
		return state, nil
	}
	var legacy olcrtcRoomAssignment
	if err := json.Unmarshal(setting.PublicValueJSON, &legacy); err != nil {
		return olcrtcRoomAssignments{}, ErrUnavailable
	}
	legacy.Room = strings.TrimSpace(legacy.Room)
	legacy.Provider = strings.TrimSpace(legacy.Provider)
	if legacy.Room == "" && legacy.Provider == "" {
		return state, nil
	}
	if legacy.Room == "" || legacy.Provider == "" || len(setting.Members) > 1 {
		return olcrtcRoomAssignments{}, ErrConflict
	}
	if len(setting.Members) == 1 {
		expected := target.SettingMemberHMAC("olcrtc")
		if _, matches := setting.Members[expected]; !matches {
			return olcrtcRoomAssignments{}, ErrConflict
		}
	}
	state.Rooms[target.Login()] = legacy
	return state, nil
}

// AssignWBRoom records a successful provider response through the canonical
// OLCRTC setting transaction, including desired state and its outbox event.
func (s *Service) AssignWBRoom(ctx context.Context, login, room, idempotencyKey string) error {
	room = strings.TrimSpace(room)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || strings.TrimSpace(login) == "" || room == "" || idempotencyKey == "" {
		return errors.New("controlplane: invalid WB room assignment")
	}
	identity, err := s.ResolveCustomerLogin(ctx, login)
	if err != nil {
		return err
	}

	setting, err := s.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	state, err := s.decodeWBRoomAssignments(ctx, setting, identity)
	if err != nil {
		return err
	}
	nextRoom := olcrtcRoomAssignment{Room: room, Provider: "wbstream"}
	if current, ok := state.Rooms[identity.Login()]; ok && current == nextRoom {
		return nil
	}
	state.Rooms[identity.Login()] = nextRoom
	members := make([]string, 0, len(state.Rooms))
	for member := range state.Rooms {
		members = append(members, member)
	}
	sort.Strings(members)

	value, err := json.Marshal(state)
	if err != nil {
		return errors.New("controlplane: encode WB room assignments")
	}
	targetValue, err := json.Marshal(nextRoom)
	if err != nil {
		return errors.New("controlplane: encode WB room assignment")
	}
	_, err = s.UpdateSetting(ctx, SettingUpdate{
		Key: "olcrtc", ExpectedGeneration: setting.Generation, PublicValueJSON: string(value),
		Members: members, Actor: "panel", CommandType: "setting.olcrtc.wbroom",
		IdempotencyKey: idempotencyKey, TargetMembers: []string{identity.Login()},
		TargetPayloads: map[string]string{identity.Login(): string(targetValue)},
	})
	return err
}
