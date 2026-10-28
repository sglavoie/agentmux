package config

import (
	"fmt"
	"time"
)

// Config stores whole seconds or milliseconds as ints. Multiplying one by its
// unit must still fit in time.Duration before any timer or threshold uses it.
const (
	maxDurationSeconds = int64(1<<63-1) / int64(time.Second)
	maxDurationMillis  = int64(1<<63-1) / int64(time.Millisecond)
)

func durationSecondsError(field string, value int) string {
	return durationValueError(field, value, maxDurationSeconds)
}

func durationMillisError(field string, value int) string {
	return durationValueError(field, value, maxDurationMillis)
}

func durationValueError(field string, value int, max int64) string {
	if value < 0 {
		return field + " must be >= 0"
	}
	if int64(value) > max {
		return fmt.Sprintf("%s must be <= %d", field, max)
	}
	return ""
}
