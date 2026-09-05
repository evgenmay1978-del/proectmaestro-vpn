package xrayclient

import (
	"context"
	"errors"

	statscommand "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
)

type statsRPC interface {
	QueryStats(context.Context, *statscommand.QueryStatsRequest, ...grpc.CallOption) (*statscommand.QueryStatsResponse, error)
}

// ManagedUserCounters returns only complete pairs for the requested managed
// identities. Xray creates counters lazily: absent pairs remain absent.
// Empty requests still require a successful StatsService query.
func (client *Client) ManagedUserCounters(ctx context.Context, users []string) (map[string][2]uint64, error) {
	if client == nil || client.stats == nil || ctx == nil {
		return nil, errors.New("xray client: StatsService unavailable")
	}
	names := make(map[string]struct{}, len(users)*2)
	for index, email := range users {
		if !managedEmail.MatchString(email) || (index > 0 && users[index-1] >= email) {
			return nil, errors.New("xray client: invalid managed counter request")
		}
		names["user>>>"+email+">>>traffic>>>uplink"] = struct{}{}
		names["user>>>"+email+">>>traffic>>>downlink"] = struct{}{}
	}
	result := make(map[string][2]uint64, len(users))
	response, err := client.stats.QueryStats(ctx, &statscommand.QueryStatsRequest{Pattern: "user>>>wl:", Reset_: false})
	if err != nil || response == nil {
		return nil, errors.New("xray client: StatsService query failed")
	}
	values := make(map[string]uint64, len(names))
	for _, stat := range response.Stat {
		if stat == nil || stat.Name == "" {
			return nil, errors.New("xray client: invalid counter data")
		}
		if _, requested := names[stat.Name]; !requested {
			continue
		}
		if _, duplicate := values[stat.Name]; duplicate || stat.Value < 0 {
			return nil, errors.New("xray client: duplicate or negative counter data")
		}
		values[stat.Name] = uint64(stat.Value)
	}
	for _, email := range users {
		uplink, upOK := values["user>>>"+email+">>>traffic>>>uplink"]
		downlink, downOK := values["user>>>"+email+">>>traffic>>>downlink"]
		if upOK && downOK {
			result[email] = [2]uint64{uplink, downlink}
		}
	}
	return result, nil
}
