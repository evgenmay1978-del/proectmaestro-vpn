package subgen

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func batchLinkNode(id, label string) WhiteListNode {
	node := xhttpLinkNode()
	node.ClientID = id
	node.Label = label
	return node
}

func TestAppendWhiteListShareLinksAppendsNodesInCallerOrder(t *testing.T) {
	ordinary := []byte("vless://ordinary-one\nhysteria2://ordinary-two")
	encoded := base64.StdEncoding.EncodeToString(ordinary)
	nodes := []WhiteListNode{
		batchLinkNode("11111111-1111-4111-8111-111111111111", "Maestro CDN — Нидерланды"),
		batchLinkNode("22222222-2222-4222-8222-222222222222", "Maestro CDN — Россия"),
	}

	augmented, err := AppendWhiteListShareLinks(encoded, nodes)
	if err != nil {
		t.Fatalf("AppendWhiteListShareLinks: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(augmented)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(decoded[:len(ordinary)], ordinary) || decoded[len(ordinary)] != '\n' {
		t.Fatalf("ordinary decoded prefix or LF changed: %q", decoded)
	}
	first, err := whiteListShareLinkWithLabel(nodes[0], nodes[0].Label)
	if err != nil {
		t.Fatal(err)
	}
	second, err := whiteListShareLinkWithLabel(nodes[1], nodes[1].Label)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(decoded), string(ordinary)+"\n"+first+"\n"+second; got != want {
		t.Fatalf("document=%q, want %q", got, want)
	}
}

func TestAppendWhiteListShareLinksRejectsInvalidBatchAtomically(t *testing.T) {
	ordinary := base64.StdEncoding.EncodeToString([]byte("vless://ordinary"))
	valid := batchLinkNode("11111111-1111-4111-8111-111111111111", "Maestro CDN — Нидерланды")
	invalid := batchLinkNode("22222222-2222-4222-8222-222222222222", "Maestro CDN — Россия")
	invalid.Path = "/invalid/%70ath"

	if got, err := AppendWhiteListShareLinks(ordinary, []WhiteListNode{valid, invalid}); err == nil || got != "" {
		t.Fatalf("invalid second node produced partial result %q, %v", got, err)
	}
}

func TestAppendWhiteListShareLinksRejectsDuplicatePublicIdentity(t *testing.T) {
	ordinary := base64.StdEncoding.EncodeToString([]byte("vless://ordinary"))
	first := batchLinkNode("11111111-1111-4111-8111-111111111111", "Maestro CDN — Нидерланды")
	duplicateLabel := batchLinkNode("22222222-2222-4222-8222-222222222222", first.Label)
	duplicateClientID := batchLinkNode(first.ClientID, "Maestro CDN — Россия")
	for _, nodes := range [][]WhiteListNode{{first, duplicateLabel}, {first, duplicateClientID}} {
		if got, err := AppendWhiteListShareLinks(ordinary, nodes); err == nil || got != "" {
			t.Fatalf("duplicate batch produced %q, %v", got, err)
		}
	}
}

func TestAppendWhiteListShareLinksEmptyAndReplayAreByteExact(t *testing.T) {
	ordinary := base64.StdEncoding.EncodeToString([]byte("vless://ordinary"))
	if got, err := AppendWhiteListShareLinks(ordinary, nil); err != nil || got != ordinary {
		t.Fatalf("empty append=%q, %v", got, err)
	}
	node := batchLinkNode("11111111-1111-4111-8111-111111111111", "Maestro CDN — Нидерланды")
	augmented, err := AppendWhiteListShareLinks(ordinary, []WhiteListNode{node})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := AppendWhiteListShareLinks(augmented, []WhiteListNode{node}); err != nil || got != augmented {
		t.Fatalf("replay=%q, %v", got, err)
	}
	if got, err := AppendWhiteListShareLinks(base64.StdEncoding.EncodeToString([]byte("vless://ordinary\r")), nil); err == nil || got != "" {
		t.Fatalf("CR ordinary accepted: %q, %v", got, err)
	}
}

func TestAppendWhiteListShareLinkRemainsLegacyCompatible(t *testing.T) {
	ordinary := base64.StdEncoding.EncodeToString([]byte("vless://ordinary"))
	node := xhttpLinkNode()
	legacy, err := AppendWhiteListShareLink(ordinary, node)
	if err != nil {
		t.Fatal(err)
	}
	link, err := WhiteListShareLink(node)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(decoded), "vless://ordinary\n"+link; got != want {
		t.Fatalf("legacy document=%q, want %q", got, want)
	}
}
