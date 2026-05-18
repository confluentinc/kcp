package utils

import (
	"fmt"
	"strconv"
	"time"
)

// ParseDurationDays parses duration strings like "1d", "7d", "30d" into time.Duration.
func ParseDurationDays(s string) (time.Duration, error) {
	if len(s) < 2 || s[len(s)-1] != 'd' {
		return 0, fmt.Errorf("must end with 'd' (e.g. 7d, 30d)")
	}
	days, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || days <= 0 {
		return 0, fmt.Errorf("invalid number of days")
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
