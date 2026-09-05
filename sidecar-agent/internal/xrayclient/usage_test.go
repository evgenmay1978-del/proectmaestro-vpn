package xrayclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	xraystats "github.com/xtls/xray-core/app/stats"
	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type localStatsRPC struct {
	statscommand.StatsServiceServer
}

func (rpc localStatsRPC) QueryStats(ctx context.Context, request *statscommand.QueryStatsRequest, _ ...grpc.CallOption) (*statscommand.QueryStatsResponse, error) {
	return rpc.StatsServiceServer.QueryStats(ctx, request)
}

type invalidStatsRPC struct {
	response *statscommand.QueryStatsResponse
	err      error
}

func (rpc invalidStatsRPC) QueryStats(context.Context, *statscommand.QueryStatsRequest, ...grpc.CallOption) (*statscommand.QueryStatsResponse, error) {
	return rpc.response, rpc.err
}

func TestManagedCountersReadWithoutResetAndIsolateMissingUsers(t *testing.T) {
	ctx := context.Background()
	manager, err := xraystats.NewManager(ctx, &xraystats.Config{})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]int64{
		"user>>>wl:one:exit-s1>>>traffic>>>uplink":   799,
		"user>>>wl:one:exit-s1>>>traffic>>>downlink": 3564,
		"user>>>wl:two:exit-s1>>>traffic>>>uplink":   12,
		"user>>>ordinary:fixed>>>traffic>>>uplink":   987,
		"user>>>canary:fixed>>>traffic>>>downlink":   654,
	}
	for name, value := range values {
		counter, err := manager.RegisterCounter(name)
		if err != nil {
			t.Fatal(err)
		}
		counter.Add(value)
	}
	client := &Client{stats: localStatsRPC{statscommand.NewStatsServer(manager)}}
	got, err := client.ManagedUserCounters(ctx, []string{"wl:one:exit-s1", "wl:two:exit-s1", "wl:unused:exit-s1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][2]uint64{"wl:one:exit-s1": {799, 3564}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed counters = %#v, want %#v", got, want)
	}
	if empty, err := client.ManagedUserCounters(ctx, nil); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty managed counters = %#v, err = %v", empty, err)
	}
	for name, value := range values {
		if got := manager.GetCounter(name).Value(); got != value {
			t.Fatalf("read reset counter %q: got %d, want %d", name, got, value)
		}
	}
	if manager.GetCounter("user>>>wl:two:exit-s1>>>traffic>>>downlink") != nil ||
		manager.GetCounter("user>>>wl:unused:exit-s1>>>traffic>>>uplink") != nil {
		t.Fatal("read fabricated missing counters")
	}
}

func TestManagedCountersEmptyUsersRejectStatsServiceFailure(t *testing.T) {
	client := &Client{stats: invalidStatsRPC{err: errors.New("StatsService unavailable")}}
	got, err := client.ManagedUserCounters(context.Background(), nil)
	if err == nil || got != nil {
		t.Fatalf("empty managed counters = %#v, err = %v; want query failure", got, err)
	}
}

func TestManagedCountersRejectCorruptRequestedStats(t *testing.T) {
	const name = "user>>>wl:one:exit-s1>>>traffic>>>uplink"
	for label, response := range map[string]*statscommand.QueryStatsResponse{
		"negative":  {Stat: []*statscommand.Stat{{Name: name, Value: -1}}},
		"duplicate": {Stat: []*statscommand.Stat{{Name: name, Value: 1}, {Name: name, Value: 2}}},
		"nil row":   {Stat: []*statscommand.Stat{nil}},
		"nil reply": nil,
	} {
		t.Run(label, func(t *testing.T) {
			client := &Client{stats: invalidStatsRPC{response: response}}
			if _, err := client.ManagedUserCounters(context.Background(), []string{"wl:one:exit-s1"}); err == nil {
				t.Fatal("corrupt counter response accepted")
			}
		})
	}
}
