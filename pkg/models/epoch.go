package models

import (
	"encoding/json"
	"time"
)

// EpochTime wraps time.Time to marshal as float seconds with 3 decimal places.
type EpochTime time.Time

// MarshalJSON implements custom JSON marshaling for EpochTime.
func (t EpochTime) MarshalJSON() ([]byte, error) {
	tt := time.Time(t)
	epochSeconds := float64(tt.UnixNano()) / 1e9
	return json.Marshal(epochSeconds)
}

// UnmarshalJSON (optional) — if you want to read it back from float too
func (t *EpochTime) UnmarshalJSON(data []byte) error {
	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		*t = EpochTime(time.Unix(sec, nsec).UTC())
		return nil
	}
	// fallback: try parsing RFC3339 if it’s a string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			*t = EpochTime(parsed)
			return nil
		}
	}
	return json.Unmarshal(data, (*time.Time)(t))
}
