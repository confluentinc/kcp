package api

import (
	"net"
	"strings"
)

// effectiveHost resolves the host the UI binds to, defaulting an empty value to
// localhost so the safe default is preserved when no --host is supplied.
func effectiveHost(host string) string {
	if strings.TrimSpace(host) == "" {
		return "localhost"
	}
	return host
}

// isLoopbackHost reports whether host is reachable only from the local machine.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// bindAddress is the host:port the UI server listens on. net.JoinHostPort
// brackets IPv6 hosts correctly (e.g. "[::1]:5556").
func bindAddress(host, port string) string {
	return net.JoinHostPort(effectiveHost(host), port)
}

// browserURL is a clickable URL for the running UI. A wildcard bind
// (0.0.0.0 / ::) isn't itself a browsable address, so point the user at
// localhost even though the server listens on every interface.
func browserURL(host, port string) string {
	h := effectiveHost(host)
	if h == "0.0.0.0" || h == "::" {
		h = "localhost"
	}
	return "http://" + net.JoinHostPort(h, port)
}

// shouldWarnNonLocalBind reports whether binding to host exposes the UI beyond
// the local machine. The UI has no authentication, so a non-loopback bind
// warrants a security warning.
func shouldWarnNonLocalBind(host string) bool {
	return !isLoopbackHost(effectiveHost(host))
}
