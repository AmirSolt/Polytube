// Package models defines data structures shared across internal packages,
// such as Event, which represents a single analytics record.
package models

import (
	"time"
)

type EventType int

const (
	EventTypeInputLog EventType = iota
	EventTypeConsoleLog
)

func (e EventType) String() string {
	switch e {
	case EventTypeInputLog:
		return "INPUT_LOG"
	case EventTypeConsoleLog:
		return "CONSOLE_LOG"
	default:
		return "UNKNOWN"
	}
}

type EventLevel int

const (
	EventLevelLog EventLevel = iota
	EventLevelWarning
	EventLevelError
	EventLevelMouse
	EventLevelKeyboard
	EventLevelJoypad
)

func (e EventLevel) String() string {
	switch e {
	case EventLevelLog:
		return "LOG"
	case EventLevelWarning:
		return "WARNING"
	case EventLevelError:
		return "ERROR"
	case EventLevelMouse:
		return "MOUSE"
	case EventLevelKeyboard:
		return "KEYBOARD"
	case EventLevelJoypad:
		return "JOYPAD"
	default:
		return "UNKNOWN"
	}
}

type Event struct {
	Timestamp  time.Time `json:"timestamp"`  // Converted from number (Unix or RFC3339)
	EventType  string    `json:"eventType"`  // "INPUT_LOG" | "CONSOLE_LOG"
	EventLevel string    `json:"eventLevel"` // "LOG" | "WARNING" | "ERROR" | "MOUSE" | "KEYBOARD" | "JOYPAD"
	Content    string    `json:"content"`
	Value      float64   `json:"value"`
}
