package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/cmd/ui/frontend"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/labstack/echo/v4"
)

type ReportService interface {
	ProcessState(state types.State) types.ProcessedState
}

type UICmdOpts struct {
	Port string
}

type UI struct {
	port          string
	reportService ReportService
}

func NewUI(reportService ReportService, opts UICmdOpts) *UI {
	return &UI{
		port:          opts.Port,
		reportService: reportService,
	}
}

func (ui *UI) Run() error {
	fmt.Println("Running UI...")

	e := echo.New()
	e.HideBanner = true

	frontend.RegisterHandlers(e)

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"status":    "healthy",
			"service":   "kcp-ui",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	e.POST("/state", ui.handleState)

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fmt.Printf("Starting UI server on %s\n", serverAddr)
	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}

func (ui *UI) handleState(c echo.Context) error {
	var state types.State

	if err := c.Bind(&state); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":   "Invalid request body",
			"message": err.Error(),
		})
	}

	processedState := ui.reportService.ProcessState(state)

	return c.JSON(http.StatusOK, processedState)
}
