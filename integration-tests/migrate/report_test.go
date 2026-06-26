//go:build integration

package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Opt-in markdown evidence report.
//
// When KCP_MATRIX_REPORT names a file path, the matrix collects a verbose
// markdown evidence report (manifest + creds + each command interleaved with its
// output, ending in a live REST read of the result) and writes it after all test
// cases run.
//
// When the env var is unset (CI default) reportEnabled is false and the matrix
// does ZERO extra work: no captured strings, no extra REST calls. Every capture
// site below is guarded by reportEnabled, and the test cases only build report
// data when it is true.
// ---------------------------------------------------------------------------

// reportPath is the destination file ("" disables the report entirely).
var reportPath = os.Getenv("KCP_MATRIX_REPORT")

// reportEnabled gates all report capture. False ⇒ behaviour is unchanged and no
// extra work is performed.
var reportEnabled = reportPath != ""

// reportCollector accumulates per-test-case sections + summary rows across all
// three areas (cluster-link auth, mirror topics, new topics). Test cases may run
// in their own subtests, so appends are mutex-guarded.
type reportCollector struct {
	mu       sync.Mutex
	sections []reportSection
}

// reportSection is one fully-built test-case section plus its summary-row fields.
type reportSection struct {
	seq      int    // global ordering key (stable, navigable output)
	category string // summary-table grouping: catClusterLink / catMirror / catNew
	mode     string // "destination" / "source" / "new"
	name     string // test case name, e.g. "D2=scram256"
	checks   string // one-sentence "what it checks"
	result   string // "✅ PASS" / "❌ FAIL"
	body     string // the full markdown section body
}

// Summary-table categories. Each test case belongs to exactly one area; the
// summary renders one table per area (see summaryGroups). mode ("destination" /
// "source") distinguishes the cluster-link topology within the cluster-link and
// mirror tables; the new-topics table has no such dimension (every case is
// mode:new with no cluster link), so it omits the Mode column.
const (
	catClusterLink = "clusterlink"
	catMirror      = "mirror"
	catNew         = "new"
)

// summaryGroups defines the per-area summary tables, in render order. showMode
// controls whether the table carries a Mode column.
var summaryGroups = []struct {
	category string
	title    string
	showMode bool
}{
	{category: catClusterLink, title: "Cluster link", showMode: true},
	{category: catMirror, title: "Mirror topics", showMode: true},
	{category: catNew, title: "New topics", showMode: false},
}

var collector = &reportCollector{}

// add appends a section. No-op when the report is disabled (defensive; callers
// already guard on reportEnabled).
func (rc *reportCollector) add(s reportSection) {
	if !reportEnabled {
		return
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.sections = append(rc.sections, s)
}

// nextSeq hands out a monotonic ordering key so sections render in a stable,
// navigable order regardless of subtest scheduling.
var reportSeqCh = func() chan int {
	ch := make(chan int)
	go func() {
		for i := 1; ; i++ {
			ch <- i
		}
	}()
	return ch
}()

func nextReportSeq() int { return <-reportSeqCh }

// render builds the full markdown document: header, per-area summary tables,
// then the per-test-case sections.
func (rc *reportCollector) render() string {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	secs := make([]reportSection, len(rc.sections))
	copy(secs, rc.sections)
	sort.Slice(secs, func(i, j int) bool { return secs[i].seq < secs[j].seq })

	var b strings.Builder
	b.WriteString("# kcp migrate verification report\n\n")
	fmt.Fprintf(&b, "Generated %s by the live `kcp migrate` verification matrix "+
		"(`make test-migrate-report`), run against the cp-server brokers in "+
		"`integration-tests/migrate/docker-compose.yml`. The matrix covers three areas: "+
		"cluster-link auth, mirror topics (destination- and source-initiated), and new "+
		"(plain, non-mirror) topics. Most test cases below run a live `kcp migrate apply`; "+
		"a few are dry-run-only or document a deferral and run no apply (their **Result** "+
		"reflects this). Each case shows the commands it ran with their output inline, ending "+
		"with the relevant live Kafka REST read: the link state (`GET …/links/<name>`) for "+
		"cluster-link cases, the link's mirrors (`GET …/links/<name>/mirrors`) for mirror "+
		"cases, and the created topics (`GET …/topics/<name>`) for new cases.\n\n",
		time.Now().Format(time.RFC1123))

	// Each section's display number is its 1-based position in the seq-sorted
	// list; numbers are stable across the summary tables and the section
	// headings/anchors below. seqNum maps seq → display number.
	seqNum := make(map[int]int, len(secs))
	for i, s := range secs {
		seqNum[s.seq] = i + 1
	}

	// Summary. One table per area (cluster-link auth, mirror topics, new topics).
	// The "#" column links to each test case's section via an explicit HTML
	// anchor (see the section loop below), so you can click a row number to jump
	// straight to that test case. rendered tracks which sections landed in a
	// table so any section with an unrecognised category surfaces in an "Other"
	// table rather than silently vanishing.
	b.WriteString("## Summary\n\n")
	rendered := make(map[int]bool, len(secs))
	for _, grp := range summaryGroups {
		rows := make([]reportSection, 0, len(secs))
		for _, s := range secs {
			if s.category == grp.category {
				rows = append(rows, s)
				rendered[s.seq] = true
			}
		}
		writeSummaryTable(&b, grp.title, rows, grp.showMode, seqNum)
	}
	// Defensive: any section not claimed by a known group (a miscategorised or
	// uncategorised case) still appears, so the report never drops a test case.
	var leftover []reportSection
	for _, s := range secs {
		if !rendered[s.seq] {
			leftover = append(leftover, s)
		}
	}
	writeSummaryTable(&b, "Other", leftover, true, seqNum)

	// Per-test-case sections, in global seq order. The display number is the
	// section's position in the seq-sorted list (matching the summary), so the
	// numbered heading + its anchor are written here (at render time), not in
	// buildSection. The `<a id="test-N">` anchor is the link target for the
	// summary's "#" column; the number is repeated in the heading so each section
	// is visually identifiable against its summary row.
	for _, s := range secs {
		n := seqNum[s.seq]
		fmt.Fprintf(&b, "<a id=\"test-%d\"></a>\n\n## %d · %s · %s\n\n", n, n, s.mode, s.name)
		b.WriteString(s.body)
		b.WriteString("\n")
	}
	return b.String()
}

// writeSummaryTable renders one per-area summary table under a "### <title>"
// heading. With showMode it includes a Mode column (destination/source); without
// it the column is omitted (the new-topics area has no cluster-link dimension).
// A nil/empty rows slice renders nothing (no heading), so empty areas — and the
// defensive "Other" bucket when all sections are categorised — leave no trace.
func writeSummaryTable(b *strings.Builder, title string, rows []reportSection, showMode bool, seqNum map[int]int) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	if showMode {
		b.WriteString("| # | Mode | Test case | Verifies | Result |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, s := range rows {
			n := seqNum[s.seq]
			fmt.Fprintf(b, "| [%d](#test-%d) | %s | %s | %s | %s |\n",
				n, n, s.mode, mdCell(s.name), mdCell(s.checks), s.result)
		}
	} else {
		b.WriteString("| # | Test case | Verifies | Result |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, s := range rows {
			n := seqNum[s.seq]
			fmt.Fprintf(b, "| [%d](#test-%d) | %s | %s | %s |\n",
				n, n, mdCell(s.name), mdCell(s.checks), s.result)
		}
	}
	b.WriteString("\n")
}

// mdCell escapes pipe characters so a value never breaks a markdown table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// The standard apply command lines. Cases with a non-standard manifest path
// (e.g. the config+drift case using m1.yaml/m2.yaml) pass their own literal
// command strings to addRun instead of using these.
const (
	applyCmd       = "kcp migrate apply -f migration.yaml"
	applyDryRunCmd = applyCmd + " --dry-run"
)

// reportStep is one stage of a test case (dry run, apply, idempotent re-apply, a
// verification read): a title, the command it ran, and the output that command
// produced. Each step renders as its own "#### <title>" sub-section under the
// section's "### Steps" heading, with the command and its output directly
// beneath — so each stage is self-contained and individually navigable.
type reportStep struct {
	title string // sub-section heading, e.g. "Dry run", "Apply", "Verify — …"
	cmd   string // the command line, shown in a shell fence
	out   string // the output it produced (omitted from the render when empty)
	lang  string // output fence language: "" for stdout, "json" for a REST read
}

// addRun appends a command-line step (dry run / apply / re-apply) and its stdout.
func (in *sectionInput) addRun(title, cmd, out string) {
	in.steps = append(in.steps, reportStep{title: title, cmd: cmd, out: out})
}

// addRead appends a verification read step: a titled `GET` command and its JSON body.
func (in *sectionInput) addRead(title, cmd, jsonBody string) {
	in.steps = append(in.steps, reportStep{title: title, cmd: cmd, out: jsonBody, lang: "json"})
}

// addReadBlock appends a verification read step built from a resultBlock (a live
// REST GET), titling the step "Verify — <label>".
func (in *sectionInput) addReadBlock(b resultBlock) {
	in.addRead("Verify — "+b.label, "GET "+b.url, b.json)
}

// TestReportRender_NumberedAnchors verifies the summary "#" column links to each
// section's anchor and that the section heading carries the same number — so a
// reader can click a summary row to jump to its test case. Pure string assembly;
// needs no broker (runs under -tags integration without the docker env).
func TestReportRender_NumberedAnchors(t *testing.T) {
	rc := &reportCollector{sections: []reportSection{
		buildSection(sectionInput{seq: 1, category: catClusterLink, mode: "destination", name: "D1=plaintext", checks: "x", manifest: "m", pass: true}),
		buildSection(sectionInput{seq: 2, category: catMirror, mode: "source", name: "mts-glob", checks: "y", manifest: "m", pass: true}),
		buildSection(sectionInput{seq: 3, category: catNew, mode: "new", name: "nt-passthrough", checks: "z", manifest: "m", pass: true}),
	}}
	out := rc.render()

	for _, want := range []string{
		"### Cluster link\n",
		"### Mirror topics\n",
		"### New topics\n",
		"| [1](#test-1) | destination | D1=plaintext |", // cluster-link row (has Mode)
		"| [2](#test-2) | source | mts-glob |",          // mirror row (has Mode)
		"| [3](#test-3) | nt-passthrough |",             // new-topics row (no Mode column)
		`<a id="test-1"></a>`,                           // section anchor (link target)
		"## 1 · destination · D1=plaintext\n",           // numbered heading matches summary #
		`<a id="test-2"></a>`,
		"## 2 · source · mts-glob\n",
		`<a id="test-3"></a>`,
		"## 3 · new · nt-passthrough\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report missing %q\n---\n%s", want, out)
		}
	}

	// The new-topics table must NOT carry a Mode column, so the "new" mode value
	// never appears as a summary cell between two pipes for that area.
	if strings.Contains(out, "| [3](#test-3) | new |") {
		t.Errorf("new-topics summary row should omit the Mode column\n---\n%s", out)
	}

	// No "Other" table when every section is categorised.
	if strings.Contains(out, "### Other\n") {
		t.Errorf("unexpected Other table — all sections were categorised\n---\n%s", out)
	}
}

// TestBuildSectionStepSubSections verifies each stage renders as its own titled
// "#### <title>" sub-section under "### Steps", with its command immediately
// followed by its output, in step order. Pure; no broker.
func TestBuildSectionStepSubSections(t *testing.T) {
	in := sectionInput{
		seq: 1, category: catMirror, mode: "destination", name: "x",
		checks: "c", manifest: "m", pass: true,
	}
	in.addRun("Dry run", applyDryRunCmd, "DRY-RUN-OUTPUT")
	in.addRun("Apply", applyCmd, "APPLY-OUTPUT")
	in.addReadBlock(resultBlock{label: "mirror topics on target", url: "http://h/mirrors", json: "READ-JSON"})
	body := buildSection(in).body

	// The steps wrapper, each step's "#### <title>" header, and each command
	// followed by its own output must appear in execution order.
	mustOrder(t, body,
		"### Steps",
		"#### Dry run", applyDryRunCmd, "DRY-RUN-OUTPUT",
		"#### Apply", applyCmd, "APPLY-OUTPUT",
		"#### Verify — mirror topics on target", "GET http://h/mirrors", "READ-JSON",
	)

	// A step with no output renders its header + command but no empty output fence.
	in2 := sectionInput{seq: 1, category: catNew, mode: "new", name: "y", checks: "c", manifest: "m", pass: true}
	in2.addRun("Deferred", "# see unit test", "")
	if !strings.Contains(buildSection(in2).body, "#### Deferred") {
		t.Errorf("output-less step should still render its header + command")
	}
}

// mustOrder asserts that the given substrings appear in body in the given order.
func mustOrder(t *testing.T, body string, want ...string) {
	t.Helper()
	idx := 0
	for _, w := range want {
		next := strings.Index(body[idx:], w)
		if next < 0 {
			t.Fatalf("substring %q not found after position %d\n---\n%s", w, idx, body)
		}
		idx += next + len(w)
	}
}

// ---------------------------------------------------------------------------
// section builder
// ---------------------------------------------------------------------------

// fencedFile labels a credentials/manifest file and fences its content.
type fencedFile struct {
	role string // e.g. "D2 link→source"
	name string // file basename
	lang string // fence language ("yaml")
	body string // file content
}

// sectionInput carries everything a test case captured for its section.
type sectionInput struct {
	seq      int
	category string        // summary grouping: catClusterLink / catMirror / catNew
	mode     string        // "destination" / "source" / "new"
	name     string        // "D2=scram256"
	checks   string        // one-sentence "what it checks" statement
	manifest string        // generated migration.yaml content
	creds    []fencedFile  //
	results  []resultBlock // non-command context: source topics, expected, notes
	steps    []reportStep  // ordered command+output pairs (the Commands & output run)
	pass     bool
	deferred bool   // case runs no live apply (documentation-only) → DEFERRED
	failMsg  string // failure detail when !pass
}

// resultBlock is one piece of evidence: either a non-command context block
// (source topics, expected outcome — url empty) shown in the Context zone, or a
// live REST read converted to a command step via addReadBlock (url set).
type resultBlock struct {
	label string // e.g. "INBOUND link on migration-dest"
	url   string // the GET URL ("" for a non-command context block)
	json  string // pretty-printed response body
}

// buildSection turns a sectionInput into a reportSection (markdown body + row).
func buildSection(in sectionInput) reportSection {
	result := "✅ PASS"
	if in.deferred {
		// A documentation-only case that runs no live apply: it neither passes nor
		// fails a live assertion, so render it as DEFERRED. A genuine failure still
		// wins (a deferred case should never have failed an assertion, but be safe).
		result = "⏭ DEFERRED"
	}
	if !in.pass {
		result = "❌ FAIL"
	}

	// NOTE: the section's H2 heading (with its display number + anchor) is written
	// by reportCollector.render at render time, because the number is the section's
	// position in the sorted summary. The body here starts at "Verifies".
	var b strings.Builder
	fmt.Fprintf(&b, "**Verifies** — %s\n\n", in.checks)

	if !in.pass && in.failMsg != "" {
		fmt.Fprintf(&b, "**Failure** — %s\n\n", in.failMsg)
	}

	b.WriteString("**Manifest** — generated `migration.yaml`:\n\n")
	fence(&b, "yaml", in.manifest)

	if len(in.creds) > 0 {
		b.WriteString("**Credential files**\n\n")
		for _, f := range in.creds {
			fmt.Fprintf(&b, "_%s — `%s`_\n\n", f.role, f.name)
			fence(&b, f.lang, f.body)
		}
	}

	// Context: non-command evidence (source topics, expected outcome, notes).
	if len(in.results) > 0 {
		b.WriteString("**Context**\n\n")
		for _, p := range in.results {
			fmt.Fprintf(&b, "_%s_\n\n", p.label)
			fence(&b, "json", p.json)
		}
	}

	// Steps: each stage (dry run, apply, idempotent re-apply, verification read)
	// is its own "#### <title>" sub-section, with the command and the output it
	// produced directly beneath — in execution order.
	if len(in.steps) > 0 {
		b.WriteString("### Steps\n\n")
		for _, s := range in.steps {
			fmt.Fprintf(&b, "#### %s\n\n", s.title)
			fence(&b, "shell", s.cmd)
			if s.out != "" {
				fence(&b, s.lang, s.out)
			}
		}
	}

	fmt.Fprintf(&b, "**Result:** %s\n", result)

	return reportSection{
		seq:      in.seq,
		category: in.category,
		mode:     in.mode,
		name:     in.name,
		checks:   in.checks,
		result:   result,
		body:     b.String(),
	}
}

// fence writes a fenced code block, trimming trailing whitespace from body.
func fence(b *strings.Builder, lang, body string) {
	b.WriteString("```")
	b.WriteString(lang)
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n```\n\n")
}

// readFileForReport reads a file's content for the report; on error returns a
// placeholder rather than failing (the report is best-effort evidence).
func readFileForReport(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("<could not read %s: %v>", path, err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// live REST link state — only ever called when reportEnabled.
// ---------------------------------------------------------------------------

// linkURL is the canonical link GET path for a cluster/name.
func linkURL(base, clusterID, name string) string {
	return base + "/kafka/v3/clusters/" + clusterID + "/links/" + name
}

// linkJSON GETs the named link and returns its pretty-printed JSON body. Used as
// report evidence; only called when reportEnabled (so it adds no work to CI).
func (c restClient) linkJSON(clusterID, name string) string {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/links/"+name)
	if err != nil {
		return fmt.Sprintf("<link GET failed: %v>", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Sprintf("<link GET decode failed (status %d): %v>", resp.StatusCode, err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}

// ---------------------------------------------------------------------------
// topic report blocks — only ever built when reportEnabled (the topic tests
// gate their section assembly on it, exactly like the auth matrix).
// ---------------------------------------------------------------------------

// prettyJSON marshals v to indented JSON for an evidence block, returning a
// placeholder on error rather than failing (the report is best-effort).
func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("<json marshal failed: %v>", err)
	}
	return string(b)
}

// topicListResult renders a resultBlock listing items (e.g. topic names, or
// name+partition structs) as pretty JSON under the given label. url may be ""
// for a block with no single REST URL (e.g. seeded source topics).
func topicListResult(label, url string, items any) resultBlock {
	return resultBlock{label: label, url: url, json: prettyJSON(items)}
}

// mirrorsResult builds a resultBlock from a live GET of the named link's mirrors
// on the target, labelled "mirror topics on target". Best-effort.
func mirrorsResult(c restClient, clusterID, link string) resultBlock {
	url := "/kafka/v3/clusters/" + clusterID + "/links/" + link + "/mirrors"
	return resultBlock{
		label: "mirror topics on target",
		url:   c.base + url,
		json:  prettyJSON(c.listMirrorTopics(clusterID, link)),
	}
}

// targetTopicsResult builds a resultBlock from a live read of each named topic on
// the target (name + partition count), labelled "topics on target". A missing
// topic is recorded with partitions -1. Best-effort.
func targetTopicsResult(c restClient, clusterID string, names []string) resultBlock {
	type topicInfo struct {
		Name       string `json:"name"`
		Partitions int    `json:"partitions"`
	}
	items := make([]topicInfo, 0, len(names))
	for _, n := range names {
		items = append(items, topicInfo{Name: n, Partitions: c.topicPartitions(clusterID, n)})
	}
	return resultBlock{
		label: "topics on target",
		url:   c.base + "/kafka/v3/clusters/" + clusterID + "/topics/{name}",
		json:  prettyJSON(items),
	}
}

// ---------------------------------------------------------------------------
// TestMain — writes the report after the matrix runs (when enabled).
// ---------------------------------------------------------------------------

func TestMain(m *testing.M) {
	code := m.Run()
	if reportEnabled {
		if err := os.WriteFile(reportPath, []byte(collector.render()), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "KCP_MATRIX_REPORT: failed to write %s: %v\n", reportPath, err)
		} else {
			fmt.Fprintf(os.Stderr, "KCP_MATRIX_REPORT: wrote evidence report to %s (%d test cases)\n",
				reportPath, len(collector.sections))
		}
	}
	os.Exit(code)
}
