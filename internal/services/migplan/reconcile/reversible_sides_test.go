package reconcile

import "testing"

func TestResolveSides(t *testing.T) {
	dynamic := reversibleGateway("cp", false)
	dynamic.Route.Mode = RouteModeDynamic

	for _, tc := range []struct {
		name       string
		gw         *GatewayConfig
		writableOn string
		want       Sides
		ok         bool
	}{
		{
			name: "normal flip toward the target", gw: reversibleGateway("cp", false), writableOn: "cc", ok: true,
			want: Sides{Writable: SideTarget, Mirror: SideSource, WritableDomain: "cc", MirrorDomain: "cp", Bound: SideSource, Moving: true},
		},
		{
			name: "back-out on the writable side", gw: reversibleGateway("cc", true), writableOn: "cc", ok: true,
			want: Sides{Writable: SideTarget, Mirror: SideSource, WritableDomain: "cc", MirrorDomain: "cp", Bound: SideTarget, Fenced: true, BackOut: true, Moving: true},
		},
		{
			name: "done: on the writable side, unfenced", gw: reversibleGateway("cc", false), writableOn: "cc", ok: true,
			want: Sides{Writable: SideTarget, Mirror: SideSource, WritableDomain: "cc", MirrorDomain: "cp", Bound: SideTarget},
		},
		{
			name: "flip toward the source", gw: reversibleGateway("cc", false), writableOn: "cp", ok: true,
			want: Sides{Writable: SideSource, Mirror: SideTarget, WritableDomain: "cp", MirrorDomain: "cc", Bound: SideTarget, Moving: true},
		},
		{name: "bound to a third domain", gw: reversibleGateway("gcp", false), writableOn: "cc"},
		{name: "writableOn names neither domain", gw: reversibleGateway("cp", false), writableOn: "x"},
		{name: "nil route", gw: &GatewayConfig{}, writableOn: "cc"},
		{name: "dynamic route", gw: dynamic, writableOn: "cc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveSides(reversibleInput(tc.writableOn), tc.gw)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (sides %+v)", ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("sides =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestSideOther(t *testing.T) {
	if SideSource.Other() != SideTarget || SideTarget.Other() != SideSource {
		t.Errorf("Other: source -> %q, target -> %q", SideSource.Other(), SideTarget.Other())
	}
}

func TestReversibleInputLabels(t *testing.T) {
	in := reversibleInput("cc")
	if got := in.ClusterLabel(SideSource); got != "the source cluster (src-id)" {
		t.Errorf("ClusterLabel(source) = %q", got)
	}
	if got := in.ClusterLabel(SideTarget); got != "the target cluster (tgt-id)" {
		t.Errorf("ClusterLabel(target) = %q", got)
	}
	if in.Domain(SideSource) != "cp" || in.Domain(SideTarget) != "cc" {
		t.Errorf("Domain: source %q, target %q", in.Domain(SideSource), in.Domain(SideTarget))
	}
}
