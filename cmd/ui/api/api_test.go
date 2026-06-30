package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

// mockReportService satisfies the ReportService interface for testing.
// The connect* fields configure FilterConnectMetrics and capture the args it was
// called with, so handler tests can assert source-type/cluster-id passthrough.
type mockReportService struct {
	connectMetrics        *types.ConnectClusterMetrics
	connectErr            error
	connectCalled         bool
	lastConnectClusterID  string
	lastConnectSourceType string

	// clusterMetrics drives FilterClusterMetrics. clusterMetricsUnfiltered is returned
	// for the handler's no-date-filter re-probe (both startTime and endTime nil) so a
	// test can model "collected, but out of the selected range"; clusterMetrics is
	// returned when a date filter is present. clusterErr, if set, is returned for all.
	clusterMetrics           *types.ProcessedClusterMetrics
	clusterMetricsUnfiltered *types.ProcessedClusterMetrics
	clusterErr               error
}

func (m *mockReportService) ProcessState(state types.State) report.ProcessedState {
	return report.ProcessedState{}
}

func (m *mockReportService) FilterRegionCosts(processedState report.ProcessedState, regionName string, startTime, endTime *time.Time) (*report.ProcessedRegionCosts, error) {
	return nil, nil
}

func (m *mockReportService) FilterMetrics(processedState report.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	return nil, nil
}

func (m *mockReportService) FilterClusterMetrics(processedState report.ProcessedState, clusterID string, sourceType string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	if m.clusterErr != nil {
		return nil, m.clusterErr
	}
	if startTime == nil && endTime == nil && m.clusterMetricsUnfiltered != nil {
		return m.clusterMetricsUnfiltered, nil
	}
	return m.clusterMetrics, nil
}

func (m *mockReportService) FilterConnectMetrics(processedState report.ProcessedState, clusterID string, sourceType string, startTime, endTime *time.Time) (*types.ConnectClusterMetrics, error) {
	m.connectCalled = true
	m.lastConnectClusterID = clusterID
	m.lastConnectSourceType = sourceType
	return m.connectMetrics, m.connectErr
}

func newTestUI() *UI {
	return &UI{
		reportService: &mockReportService{},
		states:        make(map[string]*types.State),
	}
}

func TestHandleUploadState_VersionMatch(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	body := `{"kcp_build_info":{"version":"` + build_info.Version + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/upload-state?sessionId=test-session", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleUploadState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	ui.statesMutex.RLock()
	_, stored := ui.states["test-session"]
	ui.statesMutex.RUnlock()
	if !stored {
		t.Error("expected state to be stored in session, but it was not")
	}
}

func TestHandleUploadState_VersionMismatch_Succeeds(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	body := `{"kcp_build_info":{"version":"0.5.0"}}`
	req := httptest.NewRequest(http.MethodPost, "/upload-state?sessionId=test-session", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleUploadState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	ui.statesMutex.RLock()
	_, stored := ui.states["test-session"]
	ui.statesMutex.RUnlock()
	if !stored {
		t.Error("expected state to be stored on version mismatch, but it was not")
	}
}

func TestHandleUploadState_EmptyVersion_Succeeds(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	body := `{"kcp_build_info":{"version":""}}`
	req := httptest.NewRequest(http.MethodPost, "/upload-state?sessionId=test-session", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleUploadState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for versionless state file, got %d", rec.Code)
	}

	ui.statesMutex.RLock()
	_, stored := ui.states["test-session"]
	ui.statesMutex.RUnlock()
	if !stored {
		t.Error("expected versionless state to be stored, but it was not")
	}
}

func TestHandleUploadState_SchemaMismatch_WithVersion_ReturnsActionableError(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	// Valid JSON with a version stamp but msk_sources as an array (type mismatch)
	body := `{"kcp_build_info":{"version":"0.5.0"},"msk_sources":["unexpected","array"]}`
	req := httptest.NewRequest(http.MethodPost, "/upload-state?sessionId=test-session", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleUploadState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	msg, _ := resp["message"].(string)
	// The file's version is surfaced via the "migrated from ..." breadcrumb. The new
	// contract gives actionable upgrade/recreate guidance instead of echoing the running
	// binary's version (superseded #308 behavior).
	if !strings.Contains(msg, "0.5.0") {
		t.Errorf("expected message to reference the file's version, got: %s", msg)
	}
	if !strings.Contains(msg, "recreate") {
		t.Errorf("expected actionable recreate guidance, got: %s", msg)
	}
}

func TestHandleUploadState_MissingSessionId(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	req := httptest.NewRequest(http.MethodPost, "/upload-state", strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleUploadState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func stateWithSources(version string) *types.State {
	return &types.State{
		KcpBuildInfo: types.KcpBuildInfo{Version: version},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{{ID: "test-cluster"}},
		},
	}
}

func TestNewUI_PreloadStateFile_VersionMatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := stateWithSources(build_info.Version)
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	ui, err := NewUI(&mockReportService{}, nil, nil, nil, UICmdOpts{StateFile: tmpFile.Name()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ui.statesMutex.RLock()
	_, loaded := ui.states["default"]
	ui.statesMutex.RUnlock()

	if !loaded {
		t.Error("expected state to be pre-loaded into default session")
	}
}

func TestNewUI_PreloadStateFile_VersionMismatch_Succeeds(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := stateWithSources("0.5.0")
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	ui, err := NewUI(&mockReportService{}, nil, nil, nil, UICmdOpts{StateFile: tmpFile.Name()})
	if err != nil {
		t.Fatalf("unexpected error on version mismatch with deserialisable file: %v", err)
	}

	ui.statesMutex.RLock()
	_, loaded := ui.states["default"]
	ui.statesMutex.RUnlock()

	if !loaded {
		t.Error("expected state to be loaded on version mismatch — different versions are allowed when file is deserialisable")
	}
}

func TestNewUI_PreloadStateFile_FileNotFound(t *testing.T) {
	_, err := NewUI(&mockReportService{}, nil, nil, nil, UICmdOpts{StateFile: "/nonexistent/state.json"})

	if err == nil {
		t.Error("expected error for missing state file, got nil")
	}
}

func TestNewUI_PreloadStateFile_NoSourcesNoSchema_ReturnsError(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := &types.State{KcpBuildInfo: types.KcpBuildInfo{Version: build_info.Version}}
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	_, err = NewUI(&mockReportService{}, nil, nil, nil, UICmdOpts{StateFile: tmpFile.Name()})
	if err == nil {
		t.Error("expected error for state file with no sources and no schema registries, got nil")
	}
}

func TestGetState_PreloadVersionMismatch_ReturnsState(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := stateWithSources("0.5.0")
	if err := state.WriteToFile(tmpFile.Name()); err != nil {
		t.Fatalf("failed to write state file: %v", err)
	}

	ui, err := NewUI(&mockReportService{}, nil, nil, nil, UICmdOpts{StateFile: tmpFile.Name()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleGetState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200 for version mismatch with valid file, got %d", rec.Code)
	}
}

func TestGetState_NoStateLoaded_ReturnsNotFound(t *testing.T) {
	ui := newTestUI()
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := ui.handleGetState(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rec.Code)
	}
}

// connectMetricsTestUI builds a UI with the given mock and a seeded session "s1"
// so getStateBySession resolves and the connect handler can run.
func connectMetricsTestUI(mock *mockReportService) *UI {
	ui := &UI{
		reportService: mock,
		states:        make(map[string]*types.State),
	}
	ui.states["s1"] = &types.State{}
	return ui
}

// callConnectHandler invokes handleGetConnectMetrics with the given path param and
// query string, returning the recorder for assertions.
func callConnectHandler(ui *UI, sourceType, query string) *httptest.ResponseRecorder {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/metrics/connect/"+sourceType+query, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("sourceType")
	c.SetParamValues(sourceType)
	_ = ui.handleGetConnectMetrics(c)
	return rec
}

func TestHandleGetConnectMetrics_OSK_ReturnsMetrics(t *testing.T) {
	mock := &mockReportService{
		connectMetrics: &types.ConnectClusterMetrics{
			Metrics: []types.ProcessedMetric{{Label: "connector-count"}},
		},
	}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "osk", "?clusterId=osk-kafka&sessionId=s1")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if mock.lastConnectSourceType != "osk" {
		t.Errorf("expected sourceType 'osk' passed to filter, got %q", mock.lastConnectSourceType)
	}
	if mock.lastConnectClusterID != "osk-kafka" {
		t.Errorf("expected clusterID 'osk-kafka' passed to filter, got %q", mock.lastConnectClusterID)
	}
}

func TestHandleGetConnectMetrics_MSK_ReturnsMetrics(t *testing.T) {
	mock := &mockReportService{
		connectMetrics: &types.ConnectClusterMetrics{
			Metrics: []types.ProcessedMetric{{Label: "connector-count"}},
		},
	}
	ui := connectMetricsTestUI(mock)

	// MSK ARNs contain ':' and '/', so the client URL-encodes them in the query value.
	arn := "arn%3Aaws%3Akafka%3Aus-east-1%3A123456789012%3Acluster%2Fmsk-kafka%2Fdef-456"
	rec := callConnectHandler(ui, "msk", "?clusterId="+arn+"&sessionId=s1")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if mock.lastConnectSourceType != "msk" {
		t.Errorf("expected sourceType 'msk' passed to filter, got %q", mock.lastConnectSourceType)
	}
	if mock.lastConnectClusterID != "arn:aws:kafka:us-east-1:123456789012:cluster/msk-kafka/def-456" {
		t.Errorf("expected decoded ARN passed to filter, got %q", mock.lastConnectClusterID)
	}
}

// Abuse case: an unknown source type is rejected with 4xx and never reaches the filter.
func TestHandleGetConnectMetrics_UnknownSourceType_Returns400(t *testing.T) {
	mock := &mockReportService{}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "foo", "?clusterId=whatever&sessionId=s1")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown source type, got %d", rec.Code)
	}
	if mock.connectCalled {
		t.Error("filter must not be called for an unknown source type")
	}
}

func TestHandleGetConnectMetrics_MissingClusterId_Returns400(t *testing.T) {
	mock := &mockReportService{}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "osk", "?sessionId=s1")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing clusterId, got %d", rec.Code)
	}
	if mock.connectCalled {
		t.Error("filter must not be called when clusterId is missing")
	}
}

// Abuse case: a malformed date range is rejected with 400 (preserved parseDateRange behavior).
func TestHandleGetConnectMetrics_MalformedDate_Returns400(t *testing.T) {
	mock := &mockReportService{}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "osk", "?clusterId=osk-kafka&sessionId=s1&startDate=not-a-date")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed date, got %d", rec.Code)
	}
}

func TestHandleGetConnectMetrics_MissingSessionId_Returns400(t *testing.T) {
	mock := &mockReportService{}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "osk", "?clusterId=osk-kafka")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing sessionId, got %d", rec.Code)
	}
}

func TestHandleGetConnectMetrics_NoMetrics_Returns404WithGuidance(t *testing.T) {
	// Filter found the cluster but it never had Connect metrics collected, signalled
	// by the ErrNoConnectMetricsCollected sentinel. Only this case gets scan guidance.
	mock := &mockReportService{connectErr: report.ErrNoConnectMetricsCollected}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "msk", "?clusterId=some-arn&sessionId=s1")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cluster with no Connect metrics, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "scan self-managed-connectors") {
		t.Errorf("expected scan-guidance message, got: %s", rec.Body.String())
	}
}

// A cluster that HAS Connect metrics but whose selected date range excludes them all
// must return 200 with an empty result (not the never-collected 404), so the UI shows
// an empty chart instead of a misleading "run a scan" error.
func TestHandleGetConnectMetrics_CollectedButOutOfRange_Returns200(t *testing.T) {
	mock := &mockReportService{connectMetrics: &types.ConnectClusterMetrics{Metrics: nil}}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "osk", "?clusterId=osk-kafka&sessionId=s1")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for collected-but-out-of-range metrics, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "scan self-managed-connectors") {
		t.Errorf("must not show scan-guidance when metrics exist but fall outside the date range: %s", rec.Body.String())
	}
}

func TestHandleGetConnectMetrics_FilterError_Returns404(t *testing.T) {
	mock := &mockReportService{connectErr: fmt.Errorf("cluster 'x' not found in msk sources")}
	ui := connectMetricsTestUI(mock)

	rec := callConnectHandler(ui, "msk", "?clusterId=x&sessionId=s1")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when filter returns not-found error, got %d", rec.Code)
	}
}

// oskMetricsTestUI builds a UI whose seeded session "s1" has a non-nil OSKSources so
// handleGetOSKMetrics passes its "no Apache Kafka sources" guard and reaches the filter.
func oskMetricsTestUI(mock *mockReportService) *UI {
	ui := &UI{
		reportService: mock,
		states:        make(map[string]*types.State),
	}
	ui.states["s1"] = &types.State{OSKSources: &types.OSKSourcesState{}}
	return ui
}

func callOSKMetricsHandler(ui *UI, query string) *httptest.ResponseRecorder {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/metrics/osk/osk-kafka"+query, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("clusterId")
	c.SetParamValues("osk-kafka")
	_ = ui.handleGetOSKMetrics(c)
	return rec
}

// A cluster that never had metrics collected returns 404 with scan guidance.
func TestHandleGetOSKMetrics_NeverCollected_Returns404WithGuidance(t *testing.T) {
	mock := &mockReportService{
		clusterMetrics:           &types.ProcessedClusterMetrics{Metrics: nil},
		clusterMetricsUnfiltered: &types.ProcessedClusterMetrics{Metrics: nil},
	}
	ui := oskMetricsTestUI(mock)

	rec := callOSKMetricsHandler(ui, "?sessionId=s1&startDate=2030-01-01T00:00:00Z&endDate=2030-01-02T00:00:00Z")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for never-collected metrics, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "scan clusters") {
		t.Errorf("expected scan-guidance message, got: %s", rec.Body.String())
	}
}

// A cluster that HAS metrics but whose selected date range excludes them all returns
// 200 with an empty result (not the never-collected 404), so the UI shows an empty
// chart rather than a misleading "run a scan" error.
func TestHandleGetOSKMetrics_CollectedButOutOfRange_Returns200(t *testing.T) {
	mock := &mockReportService{
		clusterMetrics: &types.ProcessedClusterMetrics{Metrics: nil}, // filtered window: empty
		clusterMetricsUnfiltered: &types.ProcessedClusterMetrics{ // unfiltered: has data
			Metrics: []types.ProcessedMetric{{Label: "BytesInPerSec"}},
		},
	}
	ui := oskMetricsTestUI(mock)

	rec := callOSKMetricsHandler(ui, "?sessionId=s1&startDate=2030-01-01T00:00:00Z&endDate=2030-01-02T00:00:00Z")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for collected-but-out-of-range metrics, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "scan clusters") {
		t.Errorf("must not show scan-guidance when metrics exist but fall outside the date range: %s", rec.Body.String())
	}
}

// Metrics present within the selected range return 200 with data and no re-probe needed.
func TestHandleGetOSKMetrics_HasData_Returns200(t *testing.T) {
	mock := &mockReportService{
		clusterMetrics: &types.ProcessedClusterMetrics{
			Metrics: []types.ProcessedMetric{{Label: "BytesInPerSec"}},
		},
	}
	ui := oskMetricsTestUI(mock)

	rec := callOSKMetricsHandler(ui, "?sessionId=s1")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for in-range metrics, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
