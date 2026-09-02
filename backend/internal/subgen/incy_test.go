package subgen

import (
	"testing"

	incylink "github.com/INCY-DEV/incy-link-encoder/go"
)

func TestIncyOfficialDeterministicVector(t *testing.T) {
	got, err := incylink.EncryptLinkDeterministic(
		"https://sub.example.com/test-vector",
		"",
		[]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b},
	)
	if err != nil {
		t.Fatal("official deterministic vector returned an error")
	}
	const want = "incy://crypt1/AAECAwQFBgcICQoLNyIQL3rDwRZqnyoD8pGKSLXP6o8NdSXQVSSALNbbUyIr__tWGFUexdIfKvvmDnuDGbmBvuppfNef6aKNZUwOm4c-Sg"
	if got != want {
		t.Fatal("official deterministic vector changed")
	}
}
