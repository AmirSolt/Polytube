// Package models defines data structures shared across internal packages,
// such as Event, which represents a single analytics record.
package models

type EventType int

const (
	EventTypeInputLog EventType = iota
	EventTypeConsoleLog
	EventTypeRecordingStarted
)

func (e EventType) String() string {
	switch e {
	case EventTypeInputLog:
		return "INPUT_LOG"
	case EventTypeConsoleLog:
		return "CONSOLE_LOG"
	case EventTypeRecordingStarted:
		return "RECORDING_STARTED"
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
	EventLevelUknownDevice
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
	case EventLevelUknownDevice:
		return "UNKNOWN_DEVICE"
	default:
		return "UNKNOWN"
	}
}

type Event struct {
	Timestamp  float64 `parquet:"name=timestamp, type=DOUBLE"`
	EventType  string  `parquet:"name=eventType, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	EventLevel string  `parquet:"name=eventLevel, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Content    string  `parquet:"name=content, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Value      float64 `parquet:"name=value, type=DOUBLE"`
}
