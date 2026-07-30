//go:build integration

package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
)

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

// reportPath is the destination file ("" disables the report entirely).
var reportPath = os.Getenv("KCP_MATRIX_REPORT")

// reportEnabled gates all report capture. False ⇒ behaviour is unchanged and no
// extra work is performed.
var reportEnabled = reportPath != ""

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

// nextReportSeq hands out a monotonic ordering key so sections render in a
// stable, navigable order regardless of subtest scheduling.
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

// readFileForReport reads a file's content for the report; on error returns a
// placeholder rather than failing (the report is best-effort evidence).
func readFileForReport(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("<could not read %s: %v>", path, err)
	}
	return string(b)
}
