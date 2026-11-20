package reconcile

import "testing"

func TestPlanModeAndRouteModeValues(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"ModeStatic", string(ModeStatic), "static"},
		{"ModeDynamic", string(ModeDynamic), "dynamic"},
		{"ModeConvert", string(ModeConvert), "convert"},
		{"RouteModeStatic", string(RouteModeStatic), "static"},
		{"RouteModeDynamic", string(RouteModeDynamic), "dynamic"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}
