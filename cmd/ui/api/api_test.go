package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

// mockReportService satisfies the ReportService interface for testing
type mockReportService struct{}

func (m *mockReportService) ProcessState(state types.State) types.ProcessedState {
	return types.ProcessedState{}
}

func (m *mockReportService) FilterRegionCosts(processedState types.ProcessedState, regionName string, startTime, endTime *time.Time) (*types.ProcessedRegionCosts, error) {
	return nil, nil
}

func (m *mockReportService) FilterMetrics(processedState types.ProcessedState, regionName, clusterName string, startTime, endTime *time.Time) (*types.ProcessedClusterMetrics, error) {
	return nil, nil
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

func TestNewUI_PreloadStateFile_VersionMatch(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := &types.State{KcpBuildInfo: types.KcpBuildInfo{Version: build_info.Version}}
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

	state := &types.State{KcpBuildInfo: types.KcpBuildInfo{Version: "0.5.0"}}
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

func TestGetState_PreloadVersionMismatch_ReturnsState(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "kcp-state-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	state := &types.State{KcpBuildInfo: types.KcpBuildInfo{Version: "0.5.0"}}
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
