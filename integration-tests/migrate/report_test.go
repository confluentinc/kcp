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
// When KCP_MATRIX_REPORT names a file path, the auth matrix collects a verbose
// markdown evidence report (manifest + creds + commands + apply output + a live
// REST GET of the resulting link state) and writes it after all test cases run.
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

// reportCollector accumulates per-test-case sections + summary rows across the
// two matrices. Test cases may run in their own subtests, so appends are
// mutex-guarded.
type reportCollector struct {
	mu       sync.Mutex
	sections []reportSection
}

// reportSection is one fully-built test-case section plus its summary-row fields.
type reportSection struct {
	seq    int    // global ordering key (stable, navigable output)
	mode   string // "destination" / "source"
	name   string // test case name, e.g. "D2=scram256"
	checks string // one-sentence "what it checks"
	result string // "✅ PASS" / "❌ FAIL"
	body   string // the full markdown section body
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

// render builds the full markdown document: header, summary table, sections.
func (rc *reportCollector) render() string {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	secs := make([]reportSection, len(rc.sections))
	copy(secs, rc.sections)
	sort.Slice(secs, func(i, j int) bool { return secs[i].seq < secs[j].seq })

	var b strings.Builder
	b.WriteString("# Cluster-link auth verification report\n\n")
	fmt.Fprintf(&b, "Generated %s by the live `kcp migrate` cluster-link auth matrix "+
		"(`make test-migrate-report`), run against the cp-server brokers in "+
		"`integration-tests/migrate/docker-compose.yml`. Every test case below is a "+
		"real `kcp migrate apply` against a real broker. The **Result** column and each test "+
		"case's **Result** section show the observed outcome of that run, including a live Kafka "+
		"REST `GET …/links/<name>` capturing the link state.\n\n",
		time.Now().Format(time.RFC1123))

	// Summary table. The "#" column links to each test case's section via an
	// explicit HTML anchor (see the section loop below), so you can click a row
	// number to jump straight to that test case.
	b.WriteString("## Summary\n\n")
	b.WriteString("| # | Mode | Test case | What it checks | Result |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for i, s := range secs {
		n := i + 1
		fmt.Fprintf(&b, "| [%d](#test-%d) | %s | %s | %s | %s |\n",
			n, n, s.mode, mdCell(s.name), mdCell(s.checks), s.result)
	}
	b.WriteString("\n")

	// Per-test-case sections. The display number is the section's position in the
	// sorted summary, so the numbered heading + its anchor are written here (at
	// render time), not in buildSection. The `<a id="test-N">` anchor is the link
	// target for the summary's "#" column; the number is repeated in the heading
	// so each section is visually identifiable against its summary row.
	for i, s := range secs {
		n := i + 1
		fmt.Fprintf(&b, "<a id=\"test-%d\"></a>\n\n## %d · %s · %s\n\n", n, n, s.mode, s.name)
		b.WriteString(s.body)
		b.WriteString("\n")
	}
	return b.String()
}

// mdCell escapes pipe characters so a value never breaks a markdown table cell.
func mdCell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// TestReportRender_NumberedAnchors verifies the summary "#" column links to each
// section's anchor and that the section heading carries the same number — so a
// reader can click a summary row to jump to its test case. Pure string assembly;
// needs no broker (runs under -tags integration without the docker env).
func TestReportRender_NumberedAnchors(t *testing.T) {
	rc := &reportCollector{sections: []reportSection{
		buildSection(sectionInput{seq: 1, mode: "destination", name: "D1=plaintext", checks: "x", manifest: "m", commands: []string{"c"}, pass: true}),
		buildSection(sectionInput{seq: 2, mode: "source", name: "mts-glob", checks: "y", manifest: "m", commands: []string{"c"}, pass: true}),
	}}
	out := rc.render()

	for _, want := range []string{
		"| [1](#test-1) | destination | D1=plaintext |", // summary row links to anchor
		"| [2](#test-2) | source | mts-glob |",
		`<a id="test-1"></a>`,                  // section anchor (link target)
		"## 1 · destination · D1=plaintext\n",  // numbered heading matches summary #
		`<a id="test-2"></a>`,
		"## 2 · source · mts-glob\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report missing %q\n---\n%s", want, out)
		}
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
	mode     string // "destination" / "source"
	name     string // "D2=scram256"
	checks   string // one-sentence "what it checks" statement
	manifest string // generated migration.yaml content
	creds    []fencedFile
	commands []string // literal commands run
	dryRun   string   // captured dry-run stdout
	apply    string   // captured apply stdout
	results  []resultBlock
	reapply  string // captured idempotent re-apply stdout
	pass     bool
	failMsg  string // failure detail when !pass
}

// resultBlock is one live REST link GET capturing the observed link state.
type resultBlock struct {
	label string // e.g. "INBOUND link on migration-dest"
	url   string // the GET URL
	json  string // pretty-printed response body
}

// buildSection turns a sectionInput into a reportSection (markdown body + row).
func buildSection(in sectionInput) reportSection {
	result := "✅ PASS"
	if !in.pass {
		result = "❌ FAIL"
	}

	// NOTE: the section's H2 heading (with its display number + anchor) is written
	// by reportCollector.render at render time, because the number is the section's
	// position in the sorted summary. The body here starts at "What this checks".
	var b strings.Builder
	fmt.Fprintf(&b, "**What this checks** — %s\n\n", in.checks)

	if !in.pass && in.failMsg != "" {
		fmt.Fprintf(&b, "**Failure** — %s\n\n", in.failMsg)
	}

	b.WriteString("**Manifest** — generated `migration.yaml`:\n\n")
	fence(&b, "yaml", in.manifest)

	b.WriteString("**Credential files**\n\n")
	for _, f := range in.creds {
		fmt.Fprintf(&b, "_%s — `%s`_\n\n", f.role, f.name)
		fence(&b, f.lang, f.body)
	}

	b.WriteString("**Commands**\n\n")
	fence(&b, "shell", strings.Join(in.commands, "\n"))

	if in.dryRun != "" {
		b.WriteString("**Dry-run output**\n\n")
		fence(&b, "", in.dryRun)
	}
	if in.apply != "" {
		b.WriteString("**Apply output**\n\n")
		fence(&b, "", in.apply)
	}

	if len(in.results) > 0 {
		b.WriteString("**Result — link state** (live REST `GET`, captured before deletion)\n\n")
		for _, p := range in.results {
			// A result block may have no single URL (e.g. a "source topics"
			// listing assembled from local fixture data); render just the label
			// in that case rather than a dangling `GET `.
			if p.url == "" {
				fmt.Fprintf(&b, "_%s_\n\n", p.label)
			} else {
				fmt.Fprintf(&b, "_%s — `GET %s`_\n\n", p.label, p.url)
			}
			fence(&b, "json", p.json)
		}
	}

	if in.reapply != "" {
		b.WriteString("**Idempotent re-apply output**\n\n")
		fence(&b, "", in.reapply)
	}

	fmt.Fprintf(&b, "**Result:** %s\n", result)

	return reportSection{
		seq:    in.seq,
		mode:   in.mode,
		name:   in.name,
		checks: in.checks,
		result: result,
		body:   b.String(),
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
