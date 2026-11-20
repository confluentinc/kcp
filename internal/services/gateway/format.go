package gateway

import "time"

// FormatElapsed formats a duration to whole seconds (e.g. "2m3s").
func FormatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}
