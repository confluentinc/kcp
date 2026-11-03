package ui

import "testing"

// parseUICmdOpts must carry the --host flag into UICmdOpts; without this the UI
// always binds the default host regardless of --host (the bug this guards).
func TestParseUICmdOptsThreadsHost(t *testing.T) {
	oldHost, oldPort := host, port
	defer func() { host, port = oldHost, oldPort }()
	host = "0.0.0.0"
	port = "15599"

	opts, err := parseUICmdOpts()
	if err != nil {
		t.Fatalf("parseUICmdOpts: %v", err)
	}
	if opts.Host != "0.0.0.0" {
		t.Errorf("opts.Host = %q, want %q", opts.Host, "0.0.0.0")
	}
	if opts.Port != "15599" {
		t.Errorf("opts.Port = %q, want %q", opts.Port, "15599")
	}
}

// The default bind must stay localhost so an upgrade never silently exposes the
// (unauthenticated) UI.
func TestUIHostFlagDefaultsToLocalhost(t *testing.T) {
	cmd := NewUICmd()
	f := cmd.Flags().Lookup("host")
	if f == nil {
		t.Fatal("ui command has no --host flag")
	}
	if f.DefValue != "localhost" {
		t.Errorf("--host default = %q, want %q", f.DefValue, "localhost")
	}
}
