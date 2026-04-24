// Package iampolicy renders AWS IAM policy documentation for commands that
// have multiple variants (e.g. create-asset target-infra, migration-infra).
// It is consumed by the aws_iam_permissions annotation on each command and
// injected into generated markdown by cmd/gen-docs.
package iampolicy

import (
	"encoding/json"
	"sort"
	"strings"
)

// Variant describes one permutation of a command's AWS IAM footprint.
type Variant struct {
	// FlagHint is the flag combination that selects this variant,
	// e.g. "--cluster-type enterprise" or "--type 4 (dedicated target)".
	// Rendered verbatim inside a ` code span ` in the subsection heading.
	FlagHint string

	// Summary is a one-line description of what this variant provisions,
	// rendered under the heading before the JSON block.
	Summary string

	// Additions are IAM actions required by this variant on top of Base.
	// May be empty — in that case the variant renders as a note rather
	// than a JSON block.
	Additions []string
}

// Render assembles the body of a cobra aws_iam_permissions annotation as
// markdown. The output is the body only; cmd/gen-docs prepends the
// "### AWS IAM Permissions" heading when it injects the section into the
// generated command reference page.
//
// Structure:
//
//	<intro>
//
//	#### Base — always required
//	```json
//	{ ...sorted, deduped base actions... }
//	```
//
//	#### Additional for `<FlagHint>`
//	<Summary>
//	```json
//	{ ...sorted, deduped additions... }
//	```
//
// Variants with empty Additions render the summary followed by
// "_No additional permissions beyond the base._" instead of a JSON block.
func Render(intro string, base []string, variants []Variant) string {
	var b strings.Builder

	if intro = strings.TrimSpace(intro); intro != "" {
		b.WriteString(intro)
		b.WriteString("\n\n")
	}

	b.WriteString("#### Base — always required\n\n")
	b.WriteString(policyBlock(base))

	for _, v := range variants {
		b.WriteString("\n#### Additional for `")
		b.WriteString(v.FlagHint)
		b.WriteString("`\n\n")
		if summary := strings.TrimSpace(v.Summary); summary != "" {
			b.WriteString(summary)
			b.WriteString("\n\n")
		}
		if len(v.Additions) == 0 {
			b.WriteString("_No additional permissions beyond the base._\n")
			continue
		}
		b.WriteString(policyBlock(v.Additions))
	}

	return b.String()
}

// policyBlock renders a fenced JSON code block containing a single-statement
// Allow policy with the given (sorted, deduped) actions and Resource "*".
// Field ordering (Version→Statement, Effect→Action→Resource) matches AWS
// documentation conventions and the hand-written discover policy.
func policyBlock(actions []string) string {
	type statement struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource string   `json:"Resource"`
	}
	type policy struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}
	out, err := json.MarshalIndent(policy{
		Version: "2012-10-17",
		Statement: []statement{{
			Effect:   "Allow",
			Action:   sortedUnique(actions),
			Resource: "*",
		}},
	}, "", "  ")
	if err != nil {
		// json.MarshalIndent over the shape above cannot fail; treat any
		// error as a programming bug so it surfaces in tests.
		panic("iampolicy: marshal policy: " + err.Error())
	}
	return "```json\n" + string(out) + "\n```\n"
}

// sortedUnique returns actions sorted alphabetically with duplicates removed.
// Returns an empty slice (not nil) when given an empty slice, so the rendered
// JSON contains `"Action": []` rather than `"Action": null` in the pathological
// zero-element case.
func sortedUnique(actions []string) []string {
	if len(actions) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(actions))
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// Union returns the sorted, deduped union of the given fragment slices.
// Helper for composing per-variant Additions from named fragment groups.
func Union(fragments ...[]string) []string {
	total := 0
	for _, f := range fragments {
		total += len(f)
	}
	merged := make([]string, 0, total)
	for _, f := range fragments {
		merged = append(merged, f...)
	}
	return sortedUnique(merged)
}

// Difference returns the sorted, deduped set of actions in `superset` that
// are not in `subset`. Helper for computing a variant's Additions from its
// full captured action set and the shared base.
func Difference(superset, subset []string) []string {
	exclude := make(map[string]struct{}, len(subset))
	for _, x := range subset {
		exclude[x] = struct{}{}
	}
	var out []string
	for _, x := range superset {
		if _, skip := exclude[x]; skip {
			continue
		}
		out = append(out, x)
	}
	return sortedUnique(out)
}

// Overlap returns the intersection of two action sets, sorted. Used by tests
// to assert that a variant's Additions do not duplicate actions already in
// the base — those should be in the base, not the additions.
func Overlap(a, b []string) []string {
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	var shared []string
	for _, x := range b {
		if _, ok := set[x]; ok {
			shared = append(shared, x)
		}
	}
	return sortedUnique(shared)
}
