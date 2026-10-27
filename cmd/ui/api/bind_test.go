package api

import "testing"

func TestEffectiveHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "localhost"},
		{"   ", "localhost"},
		{"localhost", "localhost"},
		{"0.0.0.0", "0.0.0.0"},
		{"192.168.1.5", "192.168.1.5"},
	}
	for _, c := range cases {
		if got := effectiveHost(c.in); got != c.want {
			t.Errorf("effectiveHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"localhost", "LOCALHOST", "127.0.0.1", "::1"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	nonLoopback := []string{"0.0.0.0", "::", "192.168.1.5", "example.com"}
	for _, h := range nonLoopback {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}

func TestBindAddress(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"localhost", "5556", "localhost:5556"},
		{"0.0.0.0", "8080", "0.0.0.0:8080"},
		{"", "5556", "localhost:5556"},
		{"::1", "5556", "[::1]:5556"},
	}
	for _, c := range cases {
		if got := bindAddress(c.host, c.port); got != c.want {
			t.Errorf("bindAddress(%q, %q) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestBrowserURL(t *testing.T) {
	cases := []struct{ host, port, want string }{
		{"localhost", "5556", "http://localhost:5556"},
		{"0.0.0.0", "5556", "http://localhost:5556"}, // wildcard isn't browsable → localhost
		{"::", "5556", "http://localhost:5556"},
		{"", "5556", "http://localhost:5556"},
		{"192.168.1.5", "5556", "http://192.168.1.5:5556"},
	}
	for _, c := range cases {
		if got := browserURL(c.host, c.port); got != c.want {
			t.Errorf("browserURL(%q, %q) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestShouldWarnNonLocalBind(t *testing.T) {
	// Loopback / default binds stay local — no warning.
	noWarn := []string{"", "   ", "localhost", "127.0.0.1", "::1"}
	for _, h := range noWarn {
		if shouldWarnNonLocalBind(h) {
			t.Errorf("shouldWarnNonLocalBind(%q) = true, want false", h)
		}
	}
	// Binds that expose the UI beyond this machine — warn.
	warn := []string{"0.0.0.0", "::", "192.168.1.5"}
	for _, h := range warn {
		if !shouldWarnNonLocalBind(h) {
			t.Errorf("shouldWarnNonLocalBind(%q) = false, want true", h)
		}
	}
}

func TestNewUIStoresHost(t *testing.T) {
	ui, err := NewUI(nil, nil, nil, nil, UICmdOpts{Host: "0.0.0.0", Port: "5556"})
	if err != nil {
		t.Fatalf("NewUI returned error: %v", err)
	}
	if ui.host != "0.0.0.0" {
		t.Errorf("ui.host = %q, want %q", ui.host, "0.0.0.0")
	}
}
