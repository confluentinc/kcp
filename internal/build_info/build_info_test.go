package build_info

import "testing"

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
		{"local build from tag", "v0.8.3-localdev", "https://confluentinc.github.io/kcp/dev/"},
		{"local build with commits after tag", "v0.8.3-5-gabcdef-localdev", "https://confluentinc.github.io/kcp/dev/"},
		{"local build dirty", "v0.8.3-5-gabcdef-dirty-localdev", "https://confluentinc.github.io/kcp/dev/"},
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

func TestIsDev(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"", true},
		{"dev", true},
		{DefaultDevVersion, true},
		{"v" + DefaultDevVersion, true},
		{"v0.8.3-localdev", true},
		{"v0.8.3-5-gabcdef-localdev", true},
		{"v0.8.3-5-gabcdef-dirty-localdev", true},
		{"abcdef1-localdev", true},
		{"1.2.3", false},
		{"v1.2.3", false},
		{"0.8.5", false},
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			if got := isDev(tc.version); got != tc.want {
				t.Errorf("isDev(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}
