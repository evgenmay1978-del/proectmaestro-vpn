package promo

import "testing"

// NormNick must strip nicks to a Hysteria/viper-safe login suffix — no ".", "@", spaces, etc.
// Regression guard for the 2026-07-02 "trial-roman.pfa" fleet-wide Hy2 crash.
func TestNormNick(t *testing.T) {
	cases := map[string]string{
		"roman.pfa":   "romanpfa",
		"Roman.PFA":   "romanpfa",
		"  Vasya  ":   "vasya",
		"a@b.c":       "abc",
		"keep-me_1":   "keep-me_1",
		"Привет":      "", // non-ASCII fully stripped → caller rejects
		"...":         "",
		"user name":   "username",
		"UPPER_case-9": "upper_case-9",
	}
	for in, want := range cases {
		if got := NormNick(in); got != want {
			t.Errorf("NormNick(%q) = %q, want %q", in, got, want)
		}
	}
}
