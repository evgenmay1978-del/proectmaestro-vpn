package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/controlplane"
	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type task3Source struct {
	snapshot WhiteListPublicationSnapshot
	err      error
	block    bool
	calls    int
}

func (s *task3Source) WhiteListPublication(ctx context.Context, _ string, _ time.Time) (WhiteListPublicationSnapshot, error) {
	s.calls++
	if s.block {
		<-ctx.Done()
		return WhiteListPublicationSnapshot{}, ctx.Err()
	}
	return s.snapshot, s.err
}

func task3Node(label string) subgen.WhiteListNode {
	extra := `{"sessionIDPlacement":"query","sessionIDKey":"auth","sessionIDLength":16,"seqPlacement":"query","seqKey":"chunk_id","uplinkHTTPMethod":"GET","uplinkDataPlacement":"body"}`
	material := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 1184))
	return subgen.WhiteListNode{
		Protocol: "vless", Network: "xhttp", Address: "cdn.example.test", Port: 443,
		TLS: true, ServerName: "cdn.example.test", Host: "cdn.example.test",
		Path: "/xhttp", Mode: "packet-up", UplinkHTTPMethod: "GET", UplinkDataPlacement: "body",
		ClientID: "11111111-1111-4111-8111-111111111111",
		Encryption: "mlkem768x25519plus.native.0rtt." + material,
		Security: "tls", ALPN: []string{"h2"}, Fingerprint: "firefox",
		Extra: url.QueryEscape(extra), Label: label,
	}
}

func task3Business(source WhiteListPublicationSource, now time.Time) *ServiceBusiness {
	return NewServiceBusiness(nil, ServiceBusinessConfig{
		Now: func() time.Time { return now }, SubscriptionTopology: subscriptionRequestTopology(),
		WhiteListPublicationSource: source, WhiteListPublicationTimeout: 20 * time.Millisecond,
	})
}

func task3Snapshot(now time.Time, verdict WhiteListPublicationVerdict) WhiteListPublicationSnapshot {
	return WhiteListPublicationSnapshot{
		Verdict: verdict, ProjectionVersion: 1, DesiredGeneration: 1,
		FreshThrough: now.Add(time.Hour), Nodes: []subgen.WhiteListNode{task3Node("Maestro CDN")},
	}
}

func TestTask3PublicationClosedVerdictsAndMalformedInputsFallback(t *testing.T) {
	now := time.Date(2035, 1, 2, 3, 4, 5, 0, time.UTC)
	ordinary := SubscriptionSnapshot{Document: []byte(base64.StdEncoding.EncodeToString([]byte("vless://ordinary"))), ContentType: "text/plain; charset=utf-8"}
	verdicts := []WhiteListPublicationVerdict{WhiteListNoEntitlement, WhiteListPrimaryExpired, WhiteListNoBalance, WhiteListProjectionStale, WhiteListProjectionPending, WhiteListReleaseMismatch, WhiteListSidecarUnavailable}
	for _, verdict := range verdicts {
		t.Run(string(verdict), func(t *testing.T) {
			got, err := task3Business(&task3Source{snapshot: task3Snapshot(now, verdict)}, now).applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{Links: true}, ordinary)
			if err != nil || string(got.Document) != string(ordinary.Document) || got.ETag != "" || got.ContentLength != 0 {
				t.Fatalf("fallback changed: %#v err=%v", got, err)
			}
		})
	}
	malformed := []struct {
		name   string
		mutate func(*WhiteListPublicationSnapshot)
	}{{"projection", func(s *WhiteListPublicationSnapshot) { s.ProjectionVersion = 0 }}, {"generation", func(s *WhiteListPublicationSnapshot) { s.DesiredGeneration = 0 }}, {"freshness", func(s *WhiteListPublicationSnapshot) { s.FreshThrough = now }}, {"nodes", func(s *WhiteListPublicationSnapshot) { s.Nodes = nil }}, {"node", func(s *WhiteListPublicationSnapshot) { s.Nodes = []subgen.WhiteListNode{{Label: "bad"}} }}}
	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			snap := task3Snapshot(now, WhiteListPublishable)
			tc.mutate(&snap)
			got, err := task3Business(&task3Source{snapshot: snap}, now).applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{Links: true}, ordinary)
			if err != nil || string(got.Document) != string(ordinary.Document) {
				t.Fatalf("malformed escaped: %q %v", got.Document, err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		source *task3Source
	}{{"error", &task3Source{err: errors.New("unavailable")}}, {"timeout", &task3Source{block: true}}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := task3Business(tc.source, now).applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{Links: true}, ordinary)
			if err != nil || string(got.Document) != string(ordinary.Document) {
				t.Fatalf("source failure escaped: %q %v", got.Document, err)
			}
		})
	}
}

func TestTask3PublicationLinksOnlyAndFinalHTTPMetadata(t *testing.T) {
	now := time.Date(2035, 1, 2, 3, 4, 5, 0, time.UTC)
	source := &task3Source{snapshot: task3Snapshot(now, WhiteListPublishable)}
	ordinary := []byte(base64.StdEncoding.EncodeToString([]byte("vless://ordinary")))
	if got, err := task3Business(nil, now).applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{Links: true}, SubscriptionSnapshot{Document: ordinary, ContentType: "text/plain; charset=utf-8"}); err != nil || string(got.Document) != string(ordinary) {
		t.Fatalf("default OFF changed links")
	}
	if got, err := task3Business(source, now).applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{}, SubscriptionSnapshot{Document: ordinary, ContentType: "text/plain; charset=utf-8"}); err != nil || string(got.Document) != string(ordinary) {
		t.Fatalf("non-links changed")
	}
	customer := controlplane.BusinessCustomer{Login: "fixture", Customer: controlplane.Customer{Status: "active", ExpiresAtUnix: now.Add(time.Hour).Unix(), Access: controlplane.CustomerAccess{Credentials: map[string]string{"vless": "11111111-1111-4111-8111-111111111111"}}}}
	b := task3Business(source, now)
	b.subscriptions = subscriptionRequestSource{customer: customer}
	h := NewControlPlane(b, Config{}).Handler()
	links := httptest.NewRecorder()
	h.ServeHTTP(links, httptest.NewRequest(http.MethodGet, "/sub/fixture-token?format=links", nil))
	wantETag := fmt.Sprintf("\"%x\"", sha256.Sum256(links.Body.Bytes()))
	decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(links.Body.String())
	if links.Code != http.StatusOK || links.Header().Get("ETag") != wantETag || links.Header().Get("Content-Length") != strconv.Itoa(links.Body.Len()) || decodeErr != nil || !strings.Contains(string(decoded), "cdn.example.test") {
		t.Fatalf("links metadata: status=%d headers=%v body=%d", links.Code, links.Header(), links.Body.Len())
	}
	publicationCalls := source.calls
	expectedBare, _, err := renderControlPlaneSubscription(customer, b.cfg.SubscriptionTopology, subscriptionRenderOptions{
		ClientRequest: true,
		Endpoint:      subscriptionEndpointBase,
	})
	if err != nil {
		t.Fatalf("render ordinary bare subscription: %v", err)
	}
	bare := httptest.NewRecorder()
	h.ServeHTTP(bare, httptest.NewRequest(http.MethodGet, "/sub/fixture-token", nil))
	if bare.Code != http.StatusOK || bare.Header().Get("ETag") != "" || bare.Header().Get("Content-Length") != "" || string(bare.Body.Bytes()) != string(expectedBare) || strings.Contains(bare.Body.String(), "cdn.example.test") || source.calls != publicationCalls {
		t.Fatalf("bare metadata changed: status=%d headers=%v", bare.Code, bare.Header())
	}
}

func TestTask3PublicationAfterOrdinaryCacheCannotResurrectClosedNode(t *testing.T) {
	now := time.Date(2035, 1, 2, 3, 4, 5, 0, time.UTC)
	ordinarySource := newSubscriptionReviewSource(now, 1, true, "11111111-1111-4111-8111-111111111111")
	publication := &task3Source{snapshot: task3Snapshot(now, WhiteListPublishable)}
	business := newSubscriptionReviewBusiness(&now, ordinarySource)
	business.cfg.WhiteListPublicationSource = publication
	business.cfg.WhiteListPublicationTimeout = 20 * time.Millisecond
	options := subscriptionReviewOptions(subscriptionEndpointBase)
	options.Links = true

	ordinary, _, err := renderControlPlaneSubscription(ordinarySource.state.Customer, business.cfg.SubscriptionTopology, options)
	if err != nil {
		t.Fatalf("render ordinary links: %v", err)
	}
	published, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil || string(published.Document) == string(ordinary) {
		t.Fatalf("publishable response was not augmented: err=%v body=%q", err, published.Document)
	}
	publishedDecoded, err := base64.StdEncoding.Strict().DecodeString(string(published.Document))
	if err != nil || !strings.Contains(string(publishedDecoded), "cdn.example.test") {
		t.Fatalf("published response missing CDN node: err=%v body=%q", err, publishedDecoded)
	}

	ordinarySource.snapshotErr = controlplane.ErrUnavailable
	publication.snapshot = task3Snapshot(now, WhiteListNoBalance)
	closed, err := business.subscriptionSnapshotForRequest(context.Background(), "review-token", options)
	if err != nil {
		t.Fatalf("closed verdict from ordinary LKG: %v", err)
	}
	if string(closed.Document) != string(ordinary) || closed.ETag != "" || closed.ContentLength != 0 {
		t.Fatalf("closed verdict resurrected augmented cache: %#v", closed)
	}
	closedDecoded, err := base64.StdEncoding.Strict().DecodeString(string(closed.Document))
	if err != nil || strings.Contains(string(closedDecoded), "cdn.example.test") {
		t.Fatalf("closed response retained CDN node: err=%v body=%q", err, closedDecoded)
	}
}
