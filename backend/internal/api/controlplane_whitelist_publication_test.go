package api

import (
 "context"
 "testing"
 "time"
 "github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/subgen"
)

type publicationFixture struct{ snapshot WhiteListPublicationSnapshot; err error }
func (f publicationFixture) WhiteListPublication(context.Context, string, time.Time) (WhiteListPublicationSnapshot, error) { return f.snapshot, f.err }

func TestWhiteListPublicationDefaultsClosedAndOnlyAugmentsLinks(t *testing.T) {
 b := NewServiceBusiness(nil, ServiceBusinessConfig{})
 ordinary := SubscriptionSnapshot{Document: []byte("dmxlc3M6Ly9vcmRpbmFyeQ=="), ContentType:"text/plain; charset=utf-8"}
 got, err := b.applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{Links:true}, ordinary)
 if err != nil || string(got.Document) != string(ordinary.Document) { t.Fatalf("default=%q %v", got.Document, err) }
 b.cfg.WhiteListPublicationSource = publicationFixture{snapshot: WhiteListPublicationSnapshot{Verdict: WhiteListPublishable, Nodes: []subgen.WhiteListNode{{}}}}
 got, _ = b.applyWhiteListPublication(context.Background(), "token", subscriptionRenderOptions{}, ordinary)
 if string(got.Document) != string(ordinary.Document) { t.Fatal("non-links changed") }
}
