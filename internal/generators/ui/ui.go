package ui

import (
	"embed"
	"fmt"
	"net/http"
	"time"

	"github.com/confluentinc/kcp/internal/generators/ui/frontend"
	"github.com/labstack/echo/v4"
)

//go:embed all:frontend
var assetsFS embed.FS

type UI struct {
	port string
}

func StartUI(port string) *UI {
	fmt.Println("Starting UI...")
	return &UI{port: port}
}

func (ui *UI) Run() error {
	fmt.Println("Running UI...")

	e := echo.New()
	e.HideBanner = true

	frontend.RegisterHandlers(e)

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]interface{}{
			"status":    "healthy",
			"service":   "kcp-ui",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	})

	serverAddr := fmt.Sprintf("localhost:%s", ui.port)
	fmt.Printf("Starting UI server on %s\n", serverAddr)
	e.Logger.Fatal(e.Start(serverAddr))

	return nil
}
