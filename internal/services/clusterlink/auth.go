package clusterlink

import (
	"encoding/base64"
	"fmt"
	"net/http"
)

// Authenticator sets request-level authentication on a clusterlink REST call.
// mTLS carries no header (it is enforced by the HTTP client's TLS transport),
// so it uses NoHeaderAuth.
type Authenticator interface {
	Apply(req *http.Request)
}

// BasicAuth sends HTTP Basic (also how CC api-key/secret are sent).
type BasicAuth struct{ Username, Password string }

func (a BasicAuth) Apply(req *http.Request) {
	token := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%s:%s", a.Username, a.Password))
	req.Header.Set("Authorization", "Basic "+token)
}

// BearerAuth sends an RBAC/OAuth bearer token.
type BearerAuth struct{ Token string }

func (a BearerAuth) Apply(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+a.Token)
}

// NoHeaderAuth sets no Authorization header (used for mTLS — auth is the client cert).
type NoHeaderAuth struct{}

func (NoHeaderAuth) Apply(*http.Request) {}
