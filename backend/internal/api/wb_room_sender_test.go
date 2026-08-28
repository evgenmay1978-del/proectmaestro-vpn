package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type wbTokenReaderStub struct{ calls int }

func (r *wbTokenReaderStub) ReadSettingSecret(context.Context, string) ([]byte, error) {
	r.calls++
	return []byte("account-token"), nil
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestWBRoomSenderUsesRQLiteTokenAndProviderContract(t *testing.T) {
	reader := &wbTokenReaderStub{}
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://stream.wb.ru/api-room/api/v2/room" {
			t.Fatalf("provider request=%s %s", request.Method, request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer account-token" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("provider headers=%v", request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		var payload map[string]string
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["roomType"] != "ROOM_TYPE_ALL_ON_SCREEN" || payload["roomPrivacy"] != "ROOM_PRIVACY_FREE" {
			t.Fatalf("provider payload=%s", body)
		}
		if strings.Contains(string(body), "account-token") || strings.Contains(string(body), "alice") {
			t.Fatalf("provider body leaked secret/login: %s", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"roomId":"room-123"}`)), Header: make(http.Header)}, nil
	})}
	sender, err := NewWBRoomSender(reader, client)
	if err != nil {
		t.Fatalf("NewWBRoomSender: %v", err)
	}
	response, err := sender.Post(context.Background(), []byte(`{"login":"alice"}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if string(response) != `{"room":"room-123"}` {
		t.Fatalf("response=%s", response)
	}
	if reader.calls != 1 {
		t.Fatalf("token reads=%d", reader.calls)
	}
}
