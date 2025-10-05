// Package events provides structured event logging in newline-delimited JSON (.ndjson).
// Each call to LogEvent writes exactly one JSON object per line, flushed to disk.
//
// Used by input and console listeners to record gameplay analytics events such as:
//
//	{"type":"input","timestamp":"2025-10-04T15:00:00Z","payload":{"key":"A"}}
//	{"type":"console","timestamp":"2025-10-04T15:00:02Z","payload":"Game started"}
package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"polytube/replay/pkg/models"
)

// EventLogger is a thread-safe NDJSON writer.
type EventLogger struct {
	File   *os.File
	Writer *bufio.Writer
	Mu     sync.Mutex
}

// NewEventLogger creates (or truncates) the NDJSON file at the given path.
// The file is opened in append mode if it already exists.
func NewEventLogger(path string) (*EventLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("event logger open: %w", err)
	}
	return &EventLogger{
		File:   file,
		Writer: bufio.NewWriter(file),
	}, nil
}

// LogEvent encodes the event as JSON and writes it as a single line.
// This method is safe for concurrent use.
func (e *EventLogger) LogEvent(event models.Event) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("event logger marshal: %w", err)
	}

	if _, err := e.Writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("event logger write: %w", err)
	}

	// Flush immediately to ensure durability (important for crash safety)
	if err := e.Writer.Flush(); err != nil {
		return fmt.Errorf("event logger flush: %w", err)
	}
	return nil
}

// Close flushes and closes the underlying file.
func (e *EventLogger) Close() error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := e.Writer.Flush(); err != nil {
		return fmt.Errorf("event logger flush: %w", err)
	}
	if err := e.File.Close(); err != nil {
		return fmt.Errorf("event logger close: %w", err)
	}
	return nil
}
