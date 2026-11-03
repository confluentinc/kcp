package gateway

import (
	"fmt"
	"time"
)

// FormatElapsed formats a duration to whole seconds (e.g. "2m3s").
func FormatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}

// FormatLag64 formats an int64 with comma separators (e.g. 21655 -> "21,655").
func FormatLag64(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
