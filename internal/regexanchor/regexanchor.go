// Package regexanchor compiles a user-supplied regular expression to an
// anchored full-match, safely. It is the single hardened implementation shared
// by the manifest validator and the reconciliation core so the two cannot
// diverge on how a topic pattern is anchored.
package regexanchor

import "regexp"

// Compile returns p compiled as an anchored full-match (\A(?:p)\z).
//
// It compiles p STANDALONE first. Splicing p directly into the wrapper is
// unsafe: a pattern carrying an unbalanced group (e.g. "foo)|(evil") would
// close the wrapper group early and promote a top-level alternation, escaping
// the anchor into a prefix/suffix match. A pattern that compiles standalone has
// balanced groups, so the subsequent wrap cannot restructure it. RE2 is
// linear-time, so there is no ReDoS surface in either compile.
func Compile(p string) (*regexp.Regexp, error) {
	if _, err := regexp.Compile(p); err != nil {
		return nil, err
	}
	return regexp.Compile(`\A(?:` + p + `)\z`)
}
