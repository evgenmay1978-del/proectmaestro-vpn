package controlplane

import "testing"

func TestWhiteListAdmissionReserveRequiresExplicitFreshMeasurement(t *testing.T) {
	const now int64 = 2_000_000
	for _, test := range []struct {
		name    string
		reserve WhiteListAdmissionReserve
		want    int64
	}{
		{"missing", WhiteListAdmissionReserve{}, 0},
		{"measured idle floor", WhiteListAdmissionReserve{MeasuredAtUnix: now - 1, ValidUntilUnix: now + 10}, 10_000_000},
		{"measured rate", WhiteListAdmissionReserve{MeasuredP999BytesPerSecond: 3_000_000, MeasuredAtUnix: now - 1, ValidUntilUnix: now + 10}, 15_000_000},
		{"expired", WhiteListAdmissionReserve{MeasuredP999BytesPerSecond: 3_000_000, MeasuredAtUnix: now - 1, ValidUntilUnix: now}, 0},
		{"future", WhiteListAdmissionReserve{MeasuredAtUnix: now + 1, ValidUntilUnix: now + 10}, 0},
		{"overflow", WhiteListAdmissionReserve{MeasuredP999BytesPerSecond: ^uint64(0), MeasuredAtUnix: now - 1, ValidUntilUnix: now + 10}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.reserve.RequiredBytes(now)
			if test.want == 0 {
				if err == nil || got != 0 {
					t.Fatalf("unverified reserve accepted: bytes=%d err=%v", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("reserve bytes=%d err=%v, want %d", got, err, test.want)
			}
		})
	}
}

func TestWhiteListObservationRequiresExactDisjointManagedCoverage(t *testing.T) {
	managed := []string{"wl:wl-ent-a:exit-nl", "wl:wl-ent-b:exit-nl"}
	if !whiteListObservationCoverage(nil, nil, nil) ||
		!whiteListObservationCoverage(managed, managed[:1], managed[1:]) {
		t.Fatal("empty health and partial counter availability must remain representable")
	}
	for _, test := range []struct{ available, unavailable []string }{
		{managed[:1], nil},
		{managed, managed[:1]},
		{[]string{managed[1], managed[0]}, nil},
		{[]string{"ordinary-user"}, managed[1:]},
		{nil, []string{managed[0], managed[0]}},
	} {
		if whiteListObservationCoverage(managed, test.available, test.unavailable) {
			t.Fatal("partial, duplicate, unsorted, or foreign-user observation accepted")
		}
	}
}
