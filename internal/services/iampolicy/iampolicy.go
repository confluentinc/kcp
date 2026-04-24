// Package iampolicy renders AWS IAM policy documentation for commands that
// have multiple variants (e.g. create-asset target-infra, migration-infra).
// It is consumed by the AnnotationKey annotation on each command and injected
// into generated markdown by cmd/gen-docs.
package iampolicy

import (
	"encoding/json"
	"sort"
	"strings"
)

// AnnotationKey is the Cobra Annotations map key under which commands publish
// their AWS IAM permissions markdown for cmd/gen-docs to pick up.
const AnnotationKey = "aws_iam_permissions"

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
	b.WriteString(policyBlock([]Statement{{Actions: base}}))

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
		b.WriteString(policyBlock([]Statement{{Actions: v.Additions}}))
	}

	return b.String()
}

// Statement describes one entry in an IAM policy's Statement array for
// commands that publish their permissions via RenderSingle / RenderStatements.
// Effect is always "Allow"; there is no legitimate reason to publish a Deny
// policy as a tooling requirement, so it's not exposed.
type Statement struct {
	// Sid is an optional label shown in the rendered JSON to help docs
	// readers map a statement to its purpose (e.g. "MSKClusterKafkaAccess").
	// Omit to skip the Sid field entirely.
	Sid string

	// Actions is the set of IAM actions this statement grants. Sorted and
	// deduped by the renderer.
	Actions []string

	// Resources scopes the statement. If empty or nil, renders as
	// "Resource": "*". If a single element, renders as a bare string. If
	// two or more, renders as a JSON array in the given order.
	Resources []string
}

// RenderSingle renders the body of a cobra aws_iam_permissions annotation
// for a command whose IAM footprint is a single Allow statement. Pass zero
// resources to produce `"Resource": "*"`, one resource for a bare string,
// or multiple for a JSON array. Actions are sorted alphabetically and
// deduped. The output is intro + fenced JSON block (no "Base/Additional"
// headings — those only make sense for variant-bearing commands).
func RenderSingle(intro string, actions []string, resources ...string) string {
	return RenderStatements(intro, []Statement{{
		Actions:   actions,
		Resources: resources,
	}})
}

// RenderStatements renders the body of a cobra aws_iam_permissions
// annotation as a fenced JSON block with one or more Allow statements.
// Use for commands whose documented policy spans multiple Sid-labeled
// statements (e.g. discover, bastion-host, scan-clusters). Actions inside
// each statement are sorted alphabetically and deduped; statement order
// is preserved from the input so operators see them in the order the
// author intended.
func RenderStatements(intro string, statements []Statement) string {
	var b strings.Builder
	if intro = strings.TrimSpace(intro); intro != "" {
		b.WriteString(intro)
		b.WriteString("\n\n")
	}
	b.WriteString(policyBlock(statements))
	return b.String()
}

// policyBlock renders a fenced JSON code block containing a policy with
// the given statements. Field ordering (Version→Statement, then
// Sid→Effect→Action→Resource) matches AWS documentation conventions and
// the hand-written policies this helper is replacing.
//
// Uses a json.Encoder with SetEscapeHTML(false) because placeholder ARNs
// like "arn:aws:kafka:<AWS REGION>:..." appear in Resource fields and
// must render as literal `<` / `>` (not `<` / `>`) so operators
// can copy-paste the JSON block.
func policyBlock(statements []Statement) string {
	type jsonStatement struct {
		Sid      string   `json:"Sid,omitempty"`
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource any      `json:"Resource"`
	}
	type policy struct {
		Version   string          `json:"Version"`
		Statement []jsonStatement `json:"Statement"`
	}
	js := make([]jsonStatement, 0, len(statements))
	for _, s := range statements {
		js = append(js, jsonStatement{
			Sid:      s.Sid,
			Effect:   "Allow",
			Action:   sortedUnique(s.Actions),
			Resource: resourceField(s.Resources),
		})
	}

	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(policy{
		Version:   "2012-10-17",
		Statement: js,
	}); err != nil {
		// Encoding the shape above cannot fail; treat any error as a
		// programming bug so it surfaces in tests.
		panic("iampolicy: marshal policy: " + err.Error())
	}
	// Encoder.Encode appends a trailing newline that the existing fenced
	// block contract does not want (we already control the newlines around
	// the block).
	return "```json\n" + strings.TrimRight(buf.String(), "\n") + "\n```\n"
}

// resourceField picks the JSON shape for the Resource field:
//   - nil or empty → "*"  (Resource: "*")
//   - single entry → that string (Resource: "arn:...")
//   - multiple     → the slice (Resource: [ ... ])
//
// AWS accepts all three shapes; matching these keeps rendered output
// terse for the common cases.
func resourceField(resources []string) any {
	switch len(resources) {
	case 0:
		return "*"
	case 1:
		return resources[0]
	default:
		return resources
	}
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
