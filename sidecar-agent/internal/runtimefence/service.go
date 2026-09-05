package runtimefence

import (
	"context"
	"encoding/json"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const Method = "/maestro.runtimefence.v1.ManagedSessions/Apply"

type service struct {
	dispatcher *Dispatcher
	inbounds   inbound.Manager
	stats      stats.Manager
}

func (s *service) Register(server *grpc.Server) {
	readOnly := &readOnlyStats{StatsServiceServer: statscommand.NewStatsServer(s.stats)}
	statscommand.RegisterStatsServiceServer(server, readOnly)
	compat := statscommand.StatsService_ServiceDesc
	compat.ServiceName = "v2ray.core.app.stats.command.StatsService"
	server.RegisterService(&compat, readOnly)
	server.RegisterService(&fenceServiceDesc, s)
}

func (s *service) Apply(ctx context.Context, request *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	var c Control
	if request == nil || decode(request.Value, &c) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid managed operation")
	}
	var user *protocol.MemoryUser
	if c.Operation == "grant" || c.Operation == "renew" {
		h, err := s.inbounds.GetHandler(ctx, ManagedInbound)
		if err != nil {
			return nil, status.Error(codes.FailedPrecondition, "managed inbound unavailable")
		}
		getter, ok := h.(proxy.GetInbound)
		if !ok {
			return nil, status.Error(codes.FailedPrecondition, "managed inbound unavailable")
		}
		manager, ok := getter.GetInbound().(proxy.UserManager)
		if !ok {
			return nil, status.Error(codes.FailedPrecondition, "managed users unavailable")
		}
		user = manager.GetUser(ctx, c.Email)
		if user == nil {
			return nil, status.Error(codes.FailedPrecondition, "managed user unavailable")
		}
		account, ok := user.Account.(*vless.MemoryAccount)
		p := s.dispatcher.policy.ForLevel(user.Level)
		if !ok || account.Reverse != nil || account.Flow != "" || !p.Stats.UserUplink || !p.Stats.UserDownlink || p.Stats.UserOnline {
			return nil, status.Error(codes.FailedPrecondition, "unsupported managed user")
		}
	}
	r, err := s.dispatcher.gate.apply(ctx, c, user, s.stats)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.FailedPrecondition, "managed operation did not complete")
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > maxMessage {
		return nil, status.Error(codes.Internal, "invalid managed receipt")
	}
	return wrapperspb.Bytes(data), nil
}

type readOnlyStats struct {
	statscommand.StatsServiceServer
}

func (s *readOnlyStats) GetStats(ctx context.Context, r *statscommand.GetStatsRequest) (*statscommand.GetStatsResponse, error) {
	if r != nil && r.Reset_ {
		return nil, status.Error(codes.PermissionDenied, "destructive stats reset disabled")
	}
	return s.StatsServiceServer.GetStats(ctx, r)
}
func (s *readOnlyStats) GetUsersStats(ctx context.Context, r *statscommand.GetUsersStatsRequest) (*statscommand.GetUsersStatsResponse, error) {
	if r != nil && r.Reset_ {
		return nil, status.Error(codes.PermissionDenied, "destructive stats reset disabled")
	}
	return s.StatsServiceServer.GetUsersStats(ctx, r)
}
func (s *readOnlyStats) QueryStats(ctx context.Context, r *statscommand.QueryStatsRequest) (*statscommand.QueryStatsResponse, error) {
	if r != nil && r.Reset_ {
		return nil, status.Error(codes.PermissionDenied, "destructive stats reset disabled")
	}
	return s.StatsServiceServer.QueryStats(ctx, r)
}

type fenceServer interface {
	Apply(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}

var fenceServiceDesc = grpc.ServiceDesc{
	ServiceName: "maestro.runtimefence.v1.ManagedSessions",
	HandlerType: (*fenceServer)(nil),
	Methods: []grpc.MethodDesc{{MethodName: "Apply", Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
		r := new(wrapperspb.BytesValue)
		if err := dec(r); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return srv.(fenceServer).Apply(ctx, r)
		}
		return interceptor(ctx, r, &grpc.UnaryServerInfo{Server: srv, FullMethod: Method}, func(ctx context.Context, req interface{}) (interface{}, error) {
			return srv.(fenceServer).Apply(ctx, req.(*wrapperspb.BytesValue))
		})
	}}},
}
