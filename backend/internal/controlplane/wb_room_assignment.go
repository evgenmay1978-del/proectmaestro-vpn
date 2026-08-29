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

func (s *Service) decodeWBRoomAssignments(setting BusinessSetting, canonicalLogin string) (olcrtcRoomAssignments, error) {
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
		for login, room := range persisted {
			canonical, err := CanonicalLoginKey(login)
			if err != nil {
				return olcrtcRoomAssignments{}, ErrUnavailable
			}
			room.Room = strings.TrimSpace(room.Room)
			room.Provider = strings.TrimSpace(room.Provider)
			if room.Room == "" || room.Provider == "" {
				return olcrtcRoomAssignments{}, ErrUnavailable
			}
			if _, duplicate := state.Rooms[canonical]; duplicate {
				return olcrtcRoomAssignments{}, ErrConflict
			}
			state.Rooms[canonical] = room
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
		expected := s.store.secrets.LookupHMAC("setting-member:olcrtc", []byte(canonicalLogin))
		if _, matches := setting.Members[expected]; !matches {
			return olcrtcRoomAssignments{}, ErrConflict
		}
	}
	state.Rooms[canonicalLogin] = legacy
	return state, nil
}

// AssignWBRoom records a successful provider response through the canonical
// OLCRTC setting transaction, including desired state and its outbox event.
func (s *Service) AssignWBRoom(ctx context.Context, login, room, idempotencyKey string) error {
	login = strings.TrimSpace(login)
	room = strings.TrimSpace(room)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if s == nil || login == "" || room == "" || idempotencyKey == "" {
		return errors.New("controlplane: invalid WB room assignment")
	}
	canonicalLogin, err := CanonicalLoginKey(login)
	if err != nil {
		return errors.New("controlplane: invalid WB room assignment")
	}

	setting, err := s.ReadBusinessSetting(ctx, "olcrtc")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	state, err := s.decodeWBRoomAssignments(setting, canonicalLogin)
	if err != nil {
		return err
	}
	nextRoom := olcrtcRoomAssignment{Room: room, Provider: "wbstream"}
	if current, ok := state.Rooms[canonicalLogin]; ok && current == nextRoom {
		return nil
	}
	state.Rooms[canonicalLogin] = nextRoom
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
		IdempotencyKey: idempotencyKey, TargetMembers: []string{canonicalLogin},
		TargetPayloads: map[string]string{canonicalLogin: string(targetValue)},
	})
	return err
}
