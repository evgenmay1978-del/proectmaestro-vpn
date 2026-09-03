package controlplane

import (
	"strconv"
	"testing"
)

func TestBuildWhiteListSidecarDesiredChangesOnlyManagedIdentityAndBumpsEveryOrigin(t *testing.T) {
	origins := []WhiteListOrigin{
		{OriginID: "origin-s2", NodeID: "s2", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("a"), Active: true, StaticUsers: []string{"canary@example.invalid", "static@example.invalid"}},
		{OriginID: "origin-s3", NodeID: "s3", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("b"), Active: true, StaticUsers: []string{"canary@example.invalid", "static@example.invalid"}},
		{OriginID: "origin-s4", NodeID: "s4", ReleaseID: "release-1", ProfileID: "profile-1", PresetID: "preset-1", ConfigDigest: testDigest("c"), Active: false},
	}
	exit := WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true}
	firstRoutes := []WhiteListManagedRoute{{EntitlementID: "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExitID: exit.ExitID}}
	first, err := BuildWhiteListSidecarDesired(nil, origins, firstRoutes, exit)
	if err != nil {
		t.Fatal(err)
	}
	previous := desiredByOrigin(first)
	secondRoutes := append(firstRoutes, WhiteListManagedRoute{EntitlementID: "wl-ent-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ExitID: exit.ExitID})
	second, err := BuildWhiteListSidecarDesired(previous, origins, secondRoutes, exit)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("active origin counts = %d/%d", len(first), len(second))
	}
	for index := range second {
		if second[index].Generation != first[index].Generation+1 {
			t.Fatalf("origin %s generation = %d, want %d", second[index].OriginID, second[index].Generation, first[index].Generation+1)
		}
		if got := second[index].StaticUsers; len(got) != 2 || got[0] != "canary@example.invalid" || got[1] != "static@example.invalid" {
			t.Fatalf("origin %s static users changed: %#v", second[index].OriginID, got)
		}
		if len(second[index].ManagedUsers) != 2 || second[index].ManagedUsers[0] != "wl:wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:exit-nl" || second[index].ManagedUsers[1] != "wl:wl-ent-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:exit-nl" {
			t.Fatalf("origin %s managed users = %#v", second[index].OriginID, second[index].ManagedUsers)
		}
		wantAction := second[index].NodeID + ":" + itoa64(second[index].Generation) + ":" + second[index].DesiredSHA256
		if second[index].Action.ActionKey != wantAction {
			t.Fatalf("action key = %q, want %q", second[index].Action.ActionKey, wantAction)
		}
	}
	third, err := BuildWhiteListSidecarDesired(
		desiredByOrigin(second), origins, secondRoutes[1:], exit,
	)
	if err != nil {
		t.Fatal(err)
	}
	for index := range third {
		if third[index].Generation != second[index].Generation+1 {
			t.Fatalf("origin %s removal generation = %d, want %d", third[index].OriginID, third[index].Generation, second[index].Generation+1)
		}
		if len(third[index].ManagedUsers) != 1 || third[index].ManagedUsers[0] != "wl:wl-ent-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:exit-nl" {
			t.Fatalf("origin %s managed users after removal = %#v", third[index].OriginID, third[index].ManagedUsers)
		}
		if got := third[index].StaticUsers; len(got) != 2 || got[0] != "canary@example.invalid" || got[1] != "static@example.invalid" {
			t.Fatalf("origin %s static users changed after removal: %#v", third[index].OriginID, got)
		}
	}
}

func TestBuildWhiteListRouteMatrixUsesOnlyExitCountryMetadata(t *testing.T) {
	origins := []WhiteListOrigin{{OriginID: "origin-s2", NodeID: "s2", Active: true}, {OriginID: "origin-s3", NodeID: "s3", Active: true}}
	routes := []WhiteListManagedRoute{{EntitlementID: "wl-ent-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExitID: "exit-nl"}}
	matrix, err := BuildWhiteListRouteMatrix(origins, routes, WhiteListExit{ExitID: "exit-nl", CountryCode: "NL", CountryLabel: "Netherlands", Healthy: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix) != 2 {
		t.Fatalf("matrix routes = %d", len(matrix))
	}
	for _, route := range matrix {
		if route.ExitID != "exit-nl" || route.PublicCountryLabel != "Netherlands" {
			t.Fatalf("route = %#v", route)
		}
	}
}

func desiredByOrigin(values []WhiteListSidecarDesired) map[string]WhiteListSidecarDesired {
	result := make(map[string]WhiteListSidecarDesired, len(values))
	for _, value := range values {
		result[value.OriginID] = value
	}
	return result
}

func testDigest(ch string) string {
	return ch + "000000000000000000000000000000000000000000000000000000000000000"
}

func itoa64(value int64) string { return strconv.FormatInt(value, 10) }
