package applyagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
)

const XUIPayloadKind = "xui-user-v1"

var ErrDriverInvalidTarget = errors.New("applyagent: invalid local driver target")

type XUIClient interface {
	Snapshot(context.Context, int) (XUIInboundSnapshot, error)
	UpsertUser(context.Context, int, XUIUserPatch) error
	DeleteUser(context.Context, int, string) error
	ObservedSHA256(context.Context, int) (string, error)
}

type XUIDriverConfig struct {
	NodeID      string
	ServiceID   string
	Endpoint    *url.URL
	InboundID   int
	PayloadKind string
	Client      XUIClient
}

type XUIRqliteComposition struct {
	NodeID      string
	ServiceID   string
	Endpoint    *url.URL
	InboundID   int
	PayloadKind string
}

type XUIInboundSnapshot struct {
	InboundID int
	Users     []XUIUser
}

type XUIUser struct {
	InboundID          int
	Login              string
	UUID               string
	SubID              string
	Flow               string
	AbsoluteExpiryUnix int64
	Generation         int64
	PayloadSHA256      string
}

type XUIUserPatch struct {
	InboundID          int
	Login              string
	UUID               string
	SubID              string
	Flow               string
	AbsoluteExpiryUnix int64
	Generation         int64
	PayloadSHA256      string
}

type xuiDriver struct {
	cfg                 XUIDriverConfig
	mu                  sync.Mutex
	wantSnapshotSHA256 string
}

type xuiPayload struct {
	Login              string `json:"login"`
	UUID               string `json:"uuid"`
	SubID              string `json:"sub_id"`
	Flow               string `json:"flow"`
	AbsoluteExpiryUnix int64  `json:"absolute_expiry_unix"`
}

func NewXUIDriver(cfg XUIDriverConfig) (Driver, error) {
	if cfg.PayloadKind == "" {
		cfg.PayloadKind = XUIPayloadKind
	}
	if strings.TrimSpace(cfg.NodeID) == "" ||
		strings.TrimSpace(cfg.ServiceID) == "" ||
		cfg.InboundID <= 0 ||
		cfg.Client == nil ||
		cfg.PayloadKind != XUIPayloadKind ||
		!isLoopbackEndpoint(cfg.Endpoint) {
		return nil, ErrDriverInvalidTarget
	}
	return &xuiDriver{cfg: cfg}, nil
}

func NewXUIDriverFromRqliteComposition(composition XUIRqliteComposition, client XUIClient) (Driver, error) {
	return NewXUIDriver(XUIDriverConfig{
		NodeID:      composition.NodeID,
		ServiceID:   composition.ServiceID,
		Endpoint:    composition.Endpoint,
		InboundID:   composition.InboundID,
		PayloadKind: composition.PayloadKind,
		Client:      client,
	})
}

func isLoopbackEndpoint(endpoint *url.URL) bool {
	if endpoint == nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return false
	}
	host := endpoint.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (d *xuiDriver) Inspect(ctx context.Context, snapshot MaterializedSnapshot) (AppliedState, error) {
	if err := d.validateSnapshot(snapshot); err != nil {
		return AppliedState{}, err
	}
	live, err := d.cfg.Client.Snapshot(ctx, d.cfg.InboundID)
	if err != nil {
		return AppliedState{}, ErrDriverInspect
	}
	observed, err := d.cfg.Client.ObservedSHA256(ctx, d.cfg.InboundID)
	if err != nil {
		return AppliedState{}, ErrDriverInspect
	}
	return AppliedState{
		SnapshotSHA256: snapshot.SnapshotSHA256,
		Healthy:        observed == snapshot.SnapshotSHA256 && d.liveMatches(snapshot, live),
	}, nil
}

func (d *xuiDriver) Prepare(ctx context.Context, snapshot MaterializedSnapshot) (PreparedChange, error) {
	if err := d.validateSnapshot(snapshot); err != nil {
		return PreparedChange{}, err
	}
	live, err := d.cfg.Client.Snapshot(ctx, d.cfg.InboundID)
	if err != nil {
		return PreparedChange{}, ErrDriverInspect
	}
	for _, entry := range snapshot.Entries {
		if entry.Tombstone {
			login, err := tombstoneLogin(entry)
			if err != nil {
				return PreparedChange{}, ErrInvalidCommand
			}
			if findXUIUser(live, login) != nil {
				if err := d.cfg.Client.DeleteUser(ctx, d.cfg.InboundID, login); err != nil {
					return PreparedChange{}, ErrDriverPrepare
				}
			}
			continue
		}
		patch, err := entryPatch(entry)
		if err != nil {
			return PreparedChange{}, ErrInvalidCommand
		}
		if sameXUIUser(findXUIUser(live, patch.Login), patch) {
			continue
		}
		if err := d.cfg.Client.UpsertUser(ctx, d.cfg.InboundID, patch); err != nil {
			return PreparedChange{}, ErrDriverPrepare
		}
	}
	observed, err := d.cfg.Client.ObservedSHA256(ctx, d.cfg.InboundID)
	if err != nil {
		return PreparedChange{}, ErrDriverPrepare
	}
	d.mu.Lock()
	d.wantSnapshotSHA256 = snapshot.SnapshotSHA256
	d.mu.Unlock()
	return PreparedChange{SnapshotSHA256: observed}, nil
}

func (d *xuiDriver) Commit(ctx context.Context, prepared PreparedChange) (AppliedState, error) {
	observed, err := d.cfg.Client.ObservedSHA256(ctx, d.cfg.InboundID)
	if err != nil {
		return AppliedState{}, ErrDriverCommit
	}
	d.mu.Lock()
	want := d.wantSnapshotSHA256
	d.mu.Unlock()
	if want == "" || observed != want {
		return AppliedState{}, ErrDriverCommit
	}
	return AppliedState{SnapshotSHA256: observed, Healthy: true}, nil
}

func (d *xuiDriver) Rollback(ctx context.Context, prepared PreparedChange) error {
	return nil
}

func (d *xuiDriver) validateSnapshot(snapshot MaterializedSnapshot) error {
	if snapshot.NodeID != d.cfg.NodeID || snapshot.ServiceID != d.cfg.ServiceID || snapshot.SnapshotSHA256 == "" {
		return ErrInvalidCommand
	}
	for _, entry := range snapshot.Entries {
		if entry.PayloadKind != d.cfg.PayloadKind ||
			entry.Generation <= 0 ||
			entry.CustomerID == "" ||
			entry.OperationID == "" {
			return ErrInvalidCommand
		}
	}
	return nil
}

func (d *xuiDriver) liveMatches(snapshot MaterializedSnapshot, live XUIInboundSnapshot) bool {
	for _, entry := range snapshot.Entries {
		if entry.Tombstone {
			login, err := tombstoneLogin(entry)
			if err != nil || findXUIUser(live, login) != nil {
				return false
			}
			continue
		}
		patch, err := entryPatch(entry)
		if err != nil || !sameXUIUser(findXUIUser(live, patch.Login), patch) {
			return false
		}
	}
	return true
}

func entryPatch(entry MaterializedEntry) (XUIUserPatch, error) {
	var payload xuiPayload
	decoder := json.NewDecoder(bytes.NewReader(entry.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil ||
		strings.TrimSpace(payload.Login) == "" ||
		strings.TrimSpace(payload.UUID) == "" ||
		strings.TrimSpace(payload.SubID) == "" ||
		payload.AbsoluteExpiryUnix <= 0 {
		return XUIUserPatch{}, ErrInvalidCommand
	}
	return XUIUserPatch{
		Login:              payload.Login,
		UUID:               payload.UUID,
		SubID:              payload.SubID,
		Flow:               payload.Flow,
		AbsoluteExpiryUnix: payload.AbsoluteExpiryUnix,
		Generation:         entry.Generation,
		PayloadSHA256:      entry.DesiredSHA256,
	}, nil
}

func tombstoneLogin(entry MaterializedEntry) (string, error) {
	var payload struct {
		Login     string `json:"login"`
		Tombstone bool   `json:"tombstone"`
	}
	if len(entry.Body) != 0 {
		_ = json.Unmarshal(entry.Body, &payload)
	}
	if payload.Login != "" {
		return payload.Login, nil
	}
	if entry.CustomerID != "" && entry.Tombstone {
		return strings.TrimPrefix(entry.CustomerID, "cust-"), nil
	}
	return "", ErrInvalidCommand
}

func findXUIUser(snapshot XUIInboundSnapshot, login string) *XUIUser {
	for index := range snapshot.Users {
		if snapshot.Users[index].Login == login {
			return &snapshot.Users[index]
		}
	}
	return nil
}

func sameXUIUser(user *XUIUser, patch XUIUserPatch) bool {
	return user != nil &&
		user.Login == patch.Login &&
		user.UUID == patch.UUID &&
		user.SubID == patch.SubID &&
		user.Flow == patch.Flow &&
		user.AbsoluteExpiryUnix == patch.AbsoluteExpiryUnix &&
		user.Generation == patch.Generation &&
		user.PayloadSHA256 == patch.PayloadSHA256
}
