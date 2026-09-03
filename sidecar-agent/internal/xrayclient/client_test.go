package xrayclient

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/xtls/xray-core/app/proxyman/command"
	xrayrouter "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	routingsession "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
)

type fakeHandlerRPC struct {
	users  []*protocol.User
	alters []*command.AlterInboundRequest
	err    error
}

func (fake *fakeHandlerRPC) GetInboundUsers(context.Context, *command.GetInboundUserRequest, ...grpc.CallOption) (*command.GetInboundUserResponse, error) {
	if fake.err != nil {
		return nil, fake.err
	}
	return &command.GetInboundUserResponse{Users: fake.users}, nil
}

func (fake *fakeHandlerRPC) AlterInbound(_ context.Context, request *command.AlterInboundRequest, _ ...grpc.CallOption) (*command.AlterInboundResponse, error) {
	fake.alters = append(fake.alters, request)
	if fake.err != nil {
		return nil, fake.err
	}
	return &command.AlterInboundResponse{}, nil
}

type fakeCredentials map[string]string

func (values fakeCredentials) Credential(_ context.Context, email string) (string, error) {
	value, ok := values[email]
	if !ok {
		return "", errors.New("credential unavailable")
	}
	return value, nil
}

func TestPinnedHandlerServiceListsAddsAndRemovesVLESSUsers(t *testing.T) {
	rpc := &fakeHandlerRPC{users: []*protocol.User{
		{Email: "ordinary:fixed", Account: serial.ToTypedMessage(&vless.Account{Id: "00000000-0000-4000-8000-000000000001"})},
		{Email: "wl:one:exit-s1", Account: serial.ToTypedMessage(&vless.Account{Id: "00000000-0000-4000-8000-000000000002"})},
	}}
	client, err := newWithHandler(rpc, fakeCredentials{
		"wl:two:exit-s1": "00000000-0000-4000-8000-000000000003",
	})
	if err != nil {
		t.Fatalf("newWithHandler: %v", err)
	}
	users, err := client.ListUsers(context.Background(), "maestro-cdn-in")
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	sort.Strings(users)
	if !reflect.DeepEqual(users, []string{"ordinary:fixed", "wl:one:exit-s1"}) {
		t.Fatalf("users = %#v", users)
	}
	if err := client.AddUser(context.Background(), "maestro-cdn-in", "wl:two:exit-s1"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if err := client.RemoveUser(context.Background(), "maestro-cdn-in", "wl:one:exit-s1"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}
	if len(rpc.alters) != 2 || rpc.alters[0].Tag != "maestro-cdn-in" || rpc.alters[1].Tag != "maestro-cdn-in" {
		t.Fatalf("AlterInbound requests = %#v", rpc.alters)
	}
	add, err := rpc.alters[0].Operation.GetInstance()
	if err != nil {
		t.Fatalf("decode add operation: %v", err)
	}
	addOperation, ok := add.(*command.AddUserOperation)
	if !ok || addOperation.User.Email != "wl:two:exit-s1" {
		t.Fatalf("add operation = %#v", add)
	}
	account, err := addOperation.User.Account.GetInstance()
	if err != nil {
		t.Fatalf("decode VLESS account: %v", err)
	}
	if value, ok := account.(*vless.Account); !ok || value.Id != "00000000-0000-4000-8000-000000000003" {
		t.Fatalf("VLESS account = %#v", account)
	}
	remove, err := rpc.alters[1].Operation.GetInstance()
	if err != nil {
		t.Fatalf("decode remove operation: %v", err)
	}
	if value, ok := remove.(*command.RemoveUserOperation); !ok || value.Email != "wl:one:exit-s1" {
		t.Fatalf("remove operation = %#v", remove)
	}
}

func TestPinnedHandlerServiceRejectsUnsafeMutationInputs(t *testing.T) {
	client, err := newWithHandler(&fakeHandlerRPC{}, fakeCredentials{})
	if err != nil {
		t.Fatalf("newWithHandler: %v", err)
	}
	for name, run := range map[string]func() error{
		"non-managed add":    func() error { return client.AddUser(context.Background(), "maestro-cdn-in", "ordinary:fixed") },
		"non-managed remove": func() error { return client.RemoveUser(context.Background(), "maestro-cdn-in", "canary:fixed") },
		"wrong inbound":      func() error { return client.RemoveUser(context.Background(), "production-in", "wl:one:exit-s1") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("unsafe HandlerService mutation accepted")
			}
		})
	}
}

func TestPinnedHandlerServiceMatchesExactManagedVLESSAccount(t *testing.T) {
	const email = "wl:account:exit-s1"
	const credential = "00000000-0000-4000-8000-000000000021"
	rpc := &fakeHandlerRPC{}
	client, err := newWithHandler(rpc, fakeCredentials{email: credential})
	if err != nil {
		t.Fatalf("newWithHandler: %v", err)
	}
	for name, account := range map[string]*serial.TypedMessage{
		"exact":      serial.ToTypedMessage(&vless.Account{Id: credential, Encryption: "none"}),
		"stale UUID": serial.ToTypedMessage(&vless.Account{Id: "00000000-0000-4000-8000-000000000022", Encryption: "none"}),
		"wrong type": serial.ToTypedMessage(&command.AddUserOperation{}),
		"missing":    nil,
	} {
		t.Run(name, func(t *testing.T) {
			rpc.users = []*protocol.User{{Email: email, Account: account}}
			matches, err := client.ManagedUserAccountMatches(context.Background(), "maestro-cdn-in", email)
			if err != nil {
				t.Fatalf("ManagedUserAccountMatches: %v", err)
			}
			if matches != (name == "exact") {
				t.Fatalf("matches = %v", matches)
			}
		})
	}
}

func TestPinnedXrayRouterSelectsEveryManagedExitAndBlocksUnsupportedIdentities(t *testing.T) {
	rules := make([]*xrayrouter.RoutingRule, 0, 5)
	for _, exitID := range []string{"exit-s1", "exit-s2", "exit-s3", "exit-s4"} {
		rules = append(rules, &xrayrouter.RoutingRule{
			TargetTag:  &xrayrouter.RoutingRule_Tag{Tag: exitID},
			InboundTag: []string{"maestro-cdn-in"},
			UserEmail:  []string{"regexp:^wl:[^:]+:" + exitID + "$"},
		})
	}
	rules = append(rules, &xrayrouter.RoutingRule{
		TargetTag:  &xrayrouter.RoutingRule_Tag{Tag: "block"},
		InboundTag: []string{"maestro-cdn-in"},
	})
	router := new(xrayrouter.Router)
	if err := router.Init(context.Background(), &xrayrouter.Config{Rule: rules}, nil, nil, nil); err != nil {
		t.Fatalf("init pinned Xray router: %v", err)
	}
	for _, test := range []struct {
		email string
		want  string
	}{
		{email: "wl:one:exit-s1", want: "exit-s1"},
		{email: "wl:two:exit-s2", want: "exit-s2"},
		{email: "wl:three:exit-s3", want: "exit-s3"},
		{email: "wl:four:exit-s4", want: "exit-s4"},
		{email: "wl:unknown:exit-s5", want: "block"},
		{email: "wl:malformed", want: "block"},
		{email: "ordinary:fixed", want: "block"},
	} {
		t.Run(test.email, func(t *testing.T) {
			ctx := &routingsession.Context{Inbound: &session.Inbound{
				Tag: "maestro-cdn-in", User: &protocol.MemoryUser{Email: test.email},
			}}
			route, err := router.PickRoute(ctx)
			if err != nil {
				t.Fatalf("PickRoute: %v", err)
			}
			if route.GetOutboundTag() != test.want {
				t.Fatalf("outbound = %q, want %q", route.GetOutboundTag(), test.want)
			}
		})
	}
}
