package reconcile

// Condition is one routing condition, projected read-only from the rules tree.
// Topics are exact names; TopicPatterns are anchored regexes; a topic matches
// the condition if it is in Topics OR matches any pattern.
type Condition struct {
	Topics        []string
	TopicPatterns []string
	Domain        string // the streamingDomain this condition routes to
}

// RouteView is the read-only projection of the route's rules the core reasons
// over. SourceDomain/TargetDomain are resolved during preconditions.
type RouteView struct {
	Conditions    []Condition
	DefaultDomain string
	SourceDomain  string
	TargetDomain  string
}
