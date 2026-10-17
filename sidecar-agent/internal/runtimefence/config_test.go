package runtimefence

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"
	handlercommand "github.com/xtls/xray-core/app/proxyman/command"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	confserial "github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
	vlessinbound "github.com/xtls/xray-core/proxy/vless/inbound"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var testRegistration struct {
	once sync.Once
	err  error
}

func registerForTest(t *testing.T) {
	t.Helper()
	testRegistration.once.Do(func() { testRegistration.err = Register() })
	if testRegistration.err != nil {
		t.Fatal(testRegistration.err)
	}
}

func inputConfig() *core.Config {
	return &core.Config{App: []*serial.TypedMessage{
		serial.ToTypedMessage(&dispatcher.Config{}),
		serial.ToTypedMessage(&commander.Config{Tag: "api", Service: []*serial.TypedMessage{serial.ToTypedMessage(&statscommand.Config{}), serial.ToTypedMessage(&handlercommand.Config{})}}),
	}, Inbound: []*core.InboundHandlerConfig{{Tag: ManagedInbound, ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{}), ProxySettings: serial.ToTypedMessage(&vlessinbound.Config{})}}}
}

func TestPrivateConfigInjectionIsExactAndAtomic(t *testing.T) {
	in := inputConfig()
	original := proto.Clone(in)
	c, err := Inject(in, strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(in, original) {
		t.Fatal("caller configuration mutated")
	}
	v, err := c.App[0].GetInstance()
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := configEnvelope(v.(*anypb.Any)); err != nil || cfg == nil {
		t.Fatal("known dispatcher envelope rejected")
	}
	a, _ := c.App[1].GetInstance()
	api := a.(*commander.Config)
	v, _ = api.Service[0].GetInstance()
	if cfg, err := configEnvelope(v.(*anypb.Any)); err != nil || cfg != nil {
		t.Fatal("known service envelope rejected")
	}
	if _, err := Inject(c, strings.Repeat("a", 64), strings.Repeat("b", 64)); err == nil {
		t.Fatal("second injection accepted")
	}
	for _, mutate := range []func(*core.Config){
		func(c *core.Config) { c.App = append(c.App, c.App[0]) },
		func(c *core.Config) { c.App = c.App[1:] },
		func(c *core.Config) {
			c.App[1] = serial.ToTypedMessage(&commander.Config{Tag: "api", Listen: "127.0.0.1:1"})
		},
		func(c *core.Config) {
			c.App[1] = serial.ToTypedMessage(&commander.Config{Tag: "api", Service: []*serial.TypedMessage{serial.ToTypedMessage(&statscommand.Config{}), serial.ToTypedMessage(&statscommand.Config{})}})
		},
		func(c *core.Config) { c.App = append(c.App, envelope("unknown", serviceConfig{Schema: 1})) },
	} {
		bad := inputConfig()
		mutate(bad)
		if _, err := Inject(bad, strings.Repeat("a", 64), strings.Repeat("b", 64)); err == nil {
			t.Fatal("unexpected/missing/duplicate config accepted")
		}
	}
}

func TestAnyFactoryRejectsUnknownValueAndDuplicateRegistration(t *testing.T) {
	registerForTest(t)
	if err := Register(); err == nil {
		t.Fatal("duplicate factory registered")
	}
	for _, a := range []*anypb.Any{
		{TypeUrl: "unknown", Value: []byte(`{"schema":1}`)},
		{TypeUrl: serviceURL, Value: []byte(`{"schema":1,"schema":1}`)},
		{TypeUrl: serviceURL, Value: []byte(`{"schema":1,"unknown":1}`)},
		{TypeUrl: serviceURL, Value: []byte(`{"schema":1} {}`)},
		{TypeUrl: dispatcherURL, Value: []byte(`{"schema":1}`)},
	} {
		if _, err := common.CreateObject(context.Background(), a); err == nil {
			t.Fatal("invalid Any envelope accepted")
		}
	}
}

func TestInjectedFactoriesResolveInActualCoreWithoutStart(t *testing.T) {
	registerForTest(t)
	// These are synthetic settings. core.New constructs handlers and resolves
	// features; Start is deliberately never called, so no listener or traffic
	// is created. The API retains its routed Commander form, with no Listen.
	const input = `{
	  "log":{"loglevel":"none"},
	  "api":{"tag":"api","services":["StatsService","HandlerService"]},
	  "stats":{},
	  "policy":{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}}},
	  "inbounds":[{"tag":"maestro-cdn-in","listen":"127.0.0.1","port":18443,
	    "protocol":"vless","settings":{"clients":[],"decryption":"none"}}],
	  "outbounds":[{"tag":"block","protocol":"blackhole"}]
	}`
	c, err := confserial.LoadJSONConfig(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	c, err = Inject(c, strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := core.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	d, ok := instance.GetFeature(routing.DispatcherType()).(*Dispatcher)
	if !ok || d.managed == nil || d.ordinary == nil || d.policy == nil || d.stats == nil {
		t.Fatal("actual core did not resolve the managed dispatcher dependencies")
	}
	if _, ok := instance.GetFeature((*commander.Commander)(nil)).(*commander.Commander); !ok {
		t.Fatal("actual core did not construct Commander with injected services")
	}
	// Construct the exact injected service again through the public core API
	// to assert its resolved fields without inspecting Commander's private
	// registry or starting its gRPC server.
	var managedService *service
	for _, app := range c.App {
		v, err := app.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		api, ok := v.(*commander.Config)
		if !ok {
			continue
		}
		for _, entry := range api.Service {
			v, err := entry.GetInstance()
			if err != nil {
				t.Fatal(err)
			}
			a, ok := v.(*anypb.Any)
			if !ok {
				continue
			}
			resolved, err := core.CreateObject(instance, a)
			if err != nil {
				t.Fatal(err)
			}
			managedService, ok = resolved.(*service)
			if !ok {
				t.Fatal("injected service resolved to an unexpected type")
			}
		}
	}
	if managedService == nil || managedService.dispatcher != d || managedService.inbounds == nil || managedService.stats != d.stats {
		t.Fatal("actual managed service did not resolve the existing core features")
	}
	server := grpc.NewServer()
	defer server.Stop()
	managedService.Register(server)
	if _, ok := server.GetServiceInfo()["maestro.runtimefence.v1.ManagedSessions"]; !ok {
		t.Fatal("resolved service did not register the managed RPC")
	}
}

func TestAllDestructiveStatsMethodsAndLegacyAlias(t *testing.T) {
	_, u, sm, _ := fixture(t)
	registerPair(t, sm, u.Email)
	name := counterName(u.Email, "uplink")
	sm.GetCounter(name).Add(23)
	s := &readOnlyStats{StatsServiceServer: statscommand.NewStatsServer(sm)}
	_, err := s.GetStats(context.Background(), &statscommand.GetStatsRequest{Name: name, Reset_: true})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("GetStats reset accepted")
	}
	_, err = s.QueryStats(context.Background(), &statscommand.QueryStatsRequest{Reset_: true})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("QueryStats reset accepted")
	}
	_, err = s.GetUsersStats(context.Background(), &statscommand.GetUsersStatsRequest{IncludeTraffic: true, Reset_: true})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("GetUsersStats reset accepted")
	}
	r, err := s.GetStats(context.Background(), &statscommand.GetStatsRequest{Name: name})
	if err != nil || r.Stat.Value != 23 {
		t.Fatal("read-only stats changed")
	}
	server := grpc.NewServer()
	defer server.Stop()
	(&service{stats: sm}).Register(server)
	for _, name := range []string{"xray.app.stats.command.StatsService", "v2ray.core.app.stats.command.StatsService", "maestro.runtimefence.v1.ManagedSessions"} {
		if _, ok := server.GetServiceInfo()[name]; !ok {
			t.Fatalf("missing service %s", name)
		}
	}
}
