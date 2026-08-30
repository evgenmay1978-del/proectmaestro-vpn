package devicelimit

import "testing"

func TestForLoginMatchesProductionPolicy(t *testing.T) {
	tests := []struct {
		name  string
		login string
		want  int
	}{
		{name: "empty uses default", login: "", want: 5},
		{name: "ordinary uses default", login: "customer", want: 5},
		{name: "wapmix is unlimited", login: "wapmix", want: 0},
		{name: "wapmixx is unlimited", login: "WAPMIXX", want: 0},
		{name: "wapmix2 is unlimited", login: "WaPmIx2", want: 0},
		{name: "strogino override", login: "strogino", want: 9},
		{name: "strogino override is case insensitive", login: "STROGINO", want: 9},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ForLogin(test.login); got != test.want {
				t.Fatalf("ForLogin(%q)=%d, want %d", test.login, got, test.want)
			}
		})
	}
}
