package build_info

import (
	"runtime/debug"
	"testing"
)

func TestDocsURLForVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"empty version resolves to dev", "", "https://confluentinc.github.io/kcp/dev/"},
		{"literal 'dev' sentinel", "dev", "https://confluentinc.github.io/kcp/dev/"},
		{"Makefile default (DefaultDevVersion)", DefaultDevVersion, "https://confluentinc.github.io/kcp/dev/"},
		{"'v'-prefixed DefaultDevVersion", "v" + DefaultDevVersion, "https://confluentinc.github.io/kcp/dev/"},
		{"plain semver", "1.2.3", "https://confluentinc.github.io/kcp/1.2.3/"},
		{"'v'-prefixed semver", "v1.2.3", "https://confluentinc.github.io/kcp/1.2.3/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := docsURLForVersion(tc.version); got != tc.want {
				t.Errorf("docsURLForVersion(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

func TestResolveVersion(t *testing.T) {
	kcpDep := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/consumer", Version: "(devel)"},
			Deps: []*debug.Module{
				{Path: "github.com/confluentinc/kcp", Version: v},
			},
		}
	}
	kcpAsMain := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main: debug.Module{Path: "github.com/confluentinc/kcp", Version: v},
		}
	}

	cases := []struct {
		name      string
		ldflagVer string
		buildInfo *debug.BuildInfo
		want      string
	}{
		{"ldflag real semver wins", "0.8.1", nil, "0.8.1"},
		{"ldflag real semver wins even when buildInfo has kcp dep", "0.8.1", kcpDep("v0.7.0"), "0.8.1"},
		{"dev ldflag + nil buildInfo falls back to ldflag", DefaultDevVersion, nil, DefaultDevVersion},
		{"dev ldflag + buildInfo with kcp as dep returns dep version", DefaultDevVersion, kcpDep("v0.8.1"), "v0.8.1"},
		{"dev ldflag + buildInfo with kcp as main returns main version", DefaultDevVersion, kcpAsMain("v0.8.1"), "v0.8.1"},
		{"dev ldflag + buildInfo with kcp main but '(devel)' falls back", DefaultDevVersion, kcpAsMain("(devel)"), DefaultDevVersion},
		{"dev ldflag + buildInfo with kcp main but empty falls back", DefaultDevVersion, kcpAsMain(""), DefaultDevVersion},
		{"dev ldflag + buildInfo with no kcp anywhere falls back", DefaultDevVersion, &debug.BuildInfo{Main: debug.Module{Path: "example.com/other"}}, DefaultDevVersion},
		{"empty ldflag treated as dev, resolves from deps", "", kcpDep("v0.8.1"), "v0.8.1"},
		{"literal 'dev' ldflag resolves from deps", "dev", kcpDep("v0.8.1"), "v0.8.1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.ldflagVer, tc.buildInfo); got != tc.want {
				t.Errorf("resolveVersion(%q, …) = %q, want %q", tc.ldflagVer, got, tc.want)
			}
		})
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical bare semver", "0.8.1", "0.8.1", true},
		{"identical v-prefixed", "v0.8.1", "v0.8.1", true},
		{"v-prefix on left only", "v0.8.1", "0.8.1", true},
		{"v-prefix on right only", "0.8.1", "v0.8.1", true},
		{"different patch versions", "0.8.1", "0.8.2", false},
		{"different patch, v-prefixed left", "v0.8.1", "0.8.2", false},
		{"both dev sentinel", DefaultDevVersion, "v" + DefaultDevVersion, true},
		{"both empty", "", "", true},
		{"empty vs versioned", "", "v0.8.1", false},
		{"v-only vs bare-only of same version", "v1.0.0", "1.0.0", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameVersion(tc.a, tc.b); got != tc.want {
				t.Errorf("SameVersion(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestIsDev(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"", true},
		{"dev", true},
		{DefaultDevVersion, true},
		{"v" + DefaultDevVersion, true},
		{"1.2.3", false},
		{"v1.2.3", false},
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			if got := isDev(tc.version); got != tc.want {
				t.Errorf("isDev(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}
