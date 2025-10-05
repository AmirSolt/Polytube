package utils

import (
	"math"
	"time"
)

func NowEpochSeconds() float64 {
	now := time.Now().UTC()
	seconds := float64(now.UnixNano()) / 1e9
	// round to 3 decimals
	return math.Round(seconds*1000) / 1000
}
