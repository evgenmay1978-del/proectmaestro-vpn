package runtimefence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"
	handlercommand "github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	vlessinbound "github.com/xtls/xray-core/proxy/vless/inbound"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	dispatcherURL = "type.maestrovpn.internal/runtimefence/dispatcher-v1"
	serviceURL    = "type.maestrovpn.internal/runtimefence/service-v1"
	maxMessage    = 4096
)

var hexDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type dispatcherConfig struct {
	Schema       int    `json:"schema"`
	BootID       string `json:"boot_id"`
	ConfigDigest string `json:"config_digest"`
}
type serviceConfig struct {
	Schema int `json:"schema"`
}

// decode rejects duplicate keys as well as unknown keys, trailing data, and
// unbounded input. These private envelopes contain only a single JSON object.
func decode(data []byte, target interface{}) error {
	if len(data) == 0 || len(data) > maxMessage {
		return errors.New("invalid envelope size")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return errors.New("invalid envelope")
	}
	seen := make(map[string]bool)
	for d.More() {
		t, err = d.Token()
		if err != nil {
			return errors.New("invalid envelope")
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return errors.New("duplicate envelope field")
		}
		seen[key] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return errors.New("invalid envelope")
		}
	}
	if _, err = d.Token(); err != nil {
		return errors.New("invalid envelope")
	}
	var extra interface{}
	if d.Decode(&extra) != io.EOF {
		return errors.New("trailing envelope data")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return errors.New("invalid envelope fields")
	}
	return nil
}

func envelope(kind string, cfg interface{}) *serial.TypedMessage {
	data, _ := json.Marshal(cfg)
	return serial.ToTypedMessage(&anypb.Any{TypeUrl: kind, Value: data})
}

func configEnvelope(a *anypb.Any) (*dispatcherConfig, error) {
	if a == nil {
		return nil, errors.New("missing private configuration")
	}
	switch a.TypeUrl {
	case dispatcherURL:
		var c dispatcherConfig
		if decode(a.Value, &c) != nil || c.Schema != 1 || !hexDigest.MatchString(c.BootID) || !hexDigest.MatchString(c.ConfigDigest) {
			return nil, errors.New("invalid dispatcher configuration")
		}
		return &c, nil
	case serviceURL:
		var c serviceConfig
		if decode(a.Value, &c) != nil || c.Schema != 1 {
			return nil, errors.New("invalid service configuration")
		}
		return nil, nil
	default:
		return nil, errors.New("unknown private configuration type")
	}
}

// Register installs the sole Any factory in this executable. A second call or
// another Any factory is an error; registration must precede core.New.
func Register() error {
	return common.RegisterConfig((*anypb.Any)(nil), func(ctx context.Context, raw interface{}) (interface{}, error) {
		a, ok := raw.(*anypb.Any)
		if !ok {
			return nil, errors.New("invalid private configuration")
		}
		cfg, err := configEnvelope(a)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			g, err := newGate(cfg.BootID, cfg.ConfigDigest)
			if err != nil {
				return nil, err
			}
			d := &Dispatcher{gate: g}
			err = core.RequireFeatures(ctx, func(om outbound.Manager, r routing.Router, pm policy.Manager, sm stats.Manager) error {
				managed, ordinary := new(dispatcher.DefaultDispatcher), new(dispatcher.DefaultDispatcher)
				if err := managed.Init(&dispatcher.Config{}, om, r, noUserStatsPolicy{pm}, sm); err != nil {
					return err
				}
				if err := ordinary.Init(&dispatcher.Config{}, om, r, pm, sm); err != nil {
					return err
				}
				d.managed, d.ordinary, d.policy, d.stats = managed, ordinary, pm, sm
				return nil
			})
			return d, err
		}
		s := new(service)
		err = core.RequireFeatures(ctx, func(r routing.Dispatcher, im inbound.Manager, sm stats.Manager) error {
			d, ok := r.(*Dispatcher)
			if !ok {
				return errors.New("managed dispatcher missing")
			}
			s.dispatcher, s.inbounds, s.stats = d, im, sm
			return nil
		})
		return s, err
	})
}

// Inject returns an atomic clone and accepts only the existing isolated API
// contract. User-supplied Any app/service objects cannot bypass this function.
func Inject(input *core.Config, bootID, configDigest string) (*core.Config, error) {
	if input == nil || !hexDigest.MatchString(bootID) || !hexDigest.MatchString(configDigest) {
		return nil, errors.New("invalid runtime binding")
	}
	c := proto.Clone(input).(*core.Config)
	dispatchers, apis, managedInbounds := 0, 0, 0
	for i, app := range c.App {
		if app == nil {
			return nil, errors.New("missing app configuration")
		}
		v, err := app.GetInstance()
		if err != nil {
			return nil, errors.New("invalid app configuration")
		}
		switch v := v.(type) {
		case *anypb.Any:
			return nil, errors.New("preexisting private configuration")
		case *dispatcher.Config:
			dispatchers++
			if proto.Size(v) != 0 {
				return nil, errors.New("unsupported dispatcher configuration")
			}
			c.App[i] = envelope(dispatcherURL, dispatcherConfig{Schema: 1, BootID: bootID, ConfigDigest: configDigest})
		case *commander.Config:
			apis++
			if v.Tag != "api" || v.Listen != "" || len(v.Service) != 2 {
				return nil, errors.New("unsupported commander configuration")
			}
			statsCount, handlerCount := 0, 0
			for j, entry := range v.Service {
				if entry == nil {
					return nil, errors.New("missing API service")
				}
				svc, err := entry.GetInstance()
				if err != nil {
					return nil, errors.New("invalid API service")
				}
				switch svc := svc.(type) {
				case *statscommand.Config:
					statsCount++
					if proto.Size(svc) != 0 {
						return nil, errors.New("unsupported stats service")
					}
					v.Service[j] = envelope(serviceURL, serviceConfig{Schema: 1})
				case *handlercommand.Config:
					handlerCount++
					if proto.Size(svc) != 0 {
						return nil, errors.New("unsupported handler service")
					}
				default:
					return nil, errors.New("unexpected API service")
				}
			}
			if statsCount != 1 || handlerCount != 1 {
				return nil, errors.New("duplicate or missing API service")
			}
			c.App[i] = serial.ToTypedMessage(v)
		default:
			if strings.Contains(app.Type, "fakedns") {
				return nil, errors.New("unsupported fake DNS feature")
			}
		}
	}
	for _, in := range c.Inbound {
		if in == nil {
			return nil, errors.New("missing inbound")
		}
		if in.Tag != ManagedInbound {
			continue
		}
		managedInbounds++
		if in.ReceiverSettings == nil || in.ProxySettings == nil {
			return nil, errors.New("missing managed inbound settings")
		}
		raw, err := in.ReceiverSettings.GetInstance()
		if err != nil {
			return nil, errors.New("invalid managed receiver")
		}
		receiver, ok := raw.(*proxyman.ReceiverConfig)
		if !ok || (receiver.SniffingSettings != nil && receiver.SniffingSettings.Enabled) {
			return nil, errors.New("unsupported managed sniffing")
		}
		raw, err = in.ProxySettings.GetInstance()
		if err != nil {
			return nil, errors.New("invalid managed proxy")
		}
		vc, ok := raw.(*vlessinbound.Config)
		if !ok || len(vc.Fallbacks) != 0 {
			return nil, errors.New("unsupported managed proxy")
		}
	}
	if dispatchers != 1 || apis != 1 || managedInbounds != 1 {
		return nil, errors.New("duplicate or missing runtime feature")
	}
	return c, nil
}
