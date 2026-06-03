package update

import (
	"regexp"
	"strings"
	"testing"
)

// TestAssetFilterFor verifies the edition-specific asset filter resolves the
// correct release binary and rejects everything else: the other edition, the
// archive variants, and other platforms. This is the core guard against a
// kcp-lite binary installing the full kcp (or vice-versa).
func TestAssetFilterFor(t *testing.T) {
	tests := []struct {
		name    string
		gov     bool
		goos    string
		goarch  string
		matches []string // asset names the filter must select
		rejects []string // asset names the filter must not select
	}{
		{
			name:   "prod linux amd64",
			gov:    false,
			goos:   "linux",
			goarch: "amd64",
			matches: []string{
				"kcp_linux_amd64",
				"KCP_LINUX_AMD64", // matcher lowercases, filter is case-insensitive
			},
			rejects: []string{
				"kcp-lite_linux_amd64",   // wrong edition
				"kcp_linux_amd64.tar.gz", // archive, not the raw binary
				"kcp_darwin_arm64",       // wrong platform
				"kcp_linux_arm64",        // wrong arch
				"kcp_linux_amd64.exe",    // windows ext on a linux filter
				// The library also tests the filter against the full browser
				// download URL; the ^ anchor must reject that form so matching
				// relies on the short asset name, not the URL.
				"https://github.com/confluentinc/kcp/releases/download/v1.2.3/kcp_linux_amd64",
			},
		},
		{
			name:   "gov linux amd64",
			gov:    true,
			goos:   "linux",
			goarch: "amd64",
			matches: []string{
				"kcp-lite_linux_amd64",
			},
			rejects: []string{
				"kcp_linux_amd64", // full edition must not match
				"kcp-lite_linux_amd64.tar.gz",
				"kcp-lite_darwin_amd64",
			},
		},
		{
			name:   "prod windows amd64",
			gov:    false,
			goos:   "windows",
			goarch: "amd64",
			matches: []string{
				"kcp_windows_amd64.exe",
				"kcp_windows_amd64", // raw, no extension, also acceptable
			},
			rejects: []string{
				"kcp-lite_windows_amd64.exe",
				"kcp_windows_amd64.zip",
			},
		},
		{
			name:   "gov darwin arm64",
			gov:    true,
			goos:   "darwin",
			goarch: "arm64",
			matches: []string{
				"kcp-lite_darwin_arm64",
			},
			rejects: []string{
				"kcp_darwin_arm64",
				"kcp-lite_darwin_amd64",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			re, err := regexp.Compile(assetFilterFor(tc.gov, tc.goos, tc.goarch))
			if err != nil {
				t.Fatalf("filter did not compile as a regexp: %v", err)
			}
			for _, m := range tc.matches {
				// The library lowercases asset names before matching.
				if !re.MatchString(strings.ToLower(m)) {
					t.Errorf("filter %q should match %q but did not", re.String(), m)
				}
			}
			for _, r := range tc.rejects {
				if re.MatchString(strings.ToLower(r)) {
					t.Errorf("filter %q should NOT match %q but did", re.String(), r)
				}
			}
		})
	}
}
