package reconcile

import "fmt"

// LinkFacts is what the existing cutover paths read from the target copy of the
// cluster link.
type LinkFacts struct {
	OffsetSyncEnabled bool
	// Mode is the link's link.mode config; "" when the configs don't report it.
	Mode string
	// Error is the link's link_error; "" when the REST response omits it.
	Error string
}

const (
	LinkOneWayCheckName  = "cluster link is not bidirectional"
	LinkHealthyCheckName = "cluster link reports no error"
)

// linkHealthy keys on link_error, never link_state: a healthy link passes
// through other states, and a broken one can keep showing ACTIVE mirrors while
// nothing replicates. Some surfaces omit the field, which also means healthy.
func linkHealthy(linkError string) bool {
	return linkError == "" || linkError == "NO_ERROR"
}

// checkLinkOneWay refuses only an explicit BIDIRECTIONAL: these paths promote
// mirror topics, and a missing mode is not evidence of a bidirectional link.
func checkLinkOneWay(mode string) PreconditionResult {
	if mode == "BIDIRECTIONAL" {
		return fail(LinkOneWayCheckName, "the cluster link is bidirectional (link.mode=BIDIRECTIONAL); this migration type promotes mirror topics and does not support a bidirectional link — declare reversibleStaticMigration instead")
	}
	return pass(LinkOneWayCheckName)
}

func checkLinkHealthy(linkError string) PreconditionResult {
	if !linkHealthy(linkError) {
		return fail(LinkHealthyCheckName, fmt.Sprintf("the cluster link reports link_error=%s, so nothing is being replicated; fix the link (credentials, connectivity) and rerun", linkError))
	}
	return pass(LinkHealthyCheckName)
}
