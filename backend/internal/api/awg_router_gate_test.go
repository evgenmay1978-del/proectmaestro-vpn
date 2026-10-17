package api

import "testing"

// Гейт AWG для роутеров (maestrovpn-router 0.1.31): awg получает ТОЛЬКО роутер с меткой движка в UA;
// старые роутеры и сторонние клиенты — нет (их sing-box 1.12.25 упал бы на «unknown endpoint type: awg»).
func TestAwgAllowedRouterUA(t *testing.T) {
	cases := []struct {
		ua   string
		want bool
	}{
		{"MaestroVPN-Router/0.1.31 (sing-box 1.12.25-mvpn-awg31; awg)", true},
		{"MaestroVPN-Router/0.1.40 (sing-box 1.12.25-mvpn-awg31; awg)", true},
		{"MaestroVPN-Router/0.1.30", false},
		{"MaestroVPN-Router/0.1.24", false},
		{"curl/8.5.0", false},
		{"Wget", false},
		{"Wget/1.21 (awg)", false},
		{"Karing/1.0 (x; awg)", false},
		{"", false},
	}
	for _, c := range cases {
		if got := awgAllowed(c.ua); got != c.want {
			t.Errorf("awgAllowed(%q) = %v, want %v", c.ua, got, c.want)
		}
	}
}

// Приложение гейтится как раньше — по versionCode; метка роутера на него не влияет.
func TestAwgAllowedAppUnchanged(t *testing.T) {
	if awgMinVC >= 999999 {
		t.Skip("MAESTRO_AWG_MIN_VC не задан — гейт приложения выключен")
	}
	if awgAllowed("SFA/1.0.106 (106; sing-box 1.14.0-alpha.31; language ru_RU)") && 106 < awgMinVC {
		t.Errorf("старое приложение не должно получать awg")
	}
}

// Рубильник MAESTRO_AWG_ROUTERS=0 выключает awg роутерам, не трогая приложение.
func TestAwgRoutersKillSwitch(t *testing.T) {
	prev := awgRoutersOff
	t.Cleanup(func() { awgRoutersOff = prev })
	awgRoutersOff = true
	if awgAllowed("MaestroVPN-Router/0.1.31 (sing-box 1.12.25-mvpn-awg31; awg)") {
		t.Errorf("при MAESTRO_AWG_ROUTERS=0 роутер не должен получать awg")
	}
}
