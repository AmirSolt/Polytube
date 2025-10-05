// Package logger implements a simple thread-safe internal logger
// that writes plain-text log lines to disk.
//
// Each log line format:
//
//	[2025-10-04T14:05:00Z] [INFO] message
//
// The logger is used by all internal components to record diagnostics
// and is uploaded last during shutdown.
package logger

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"time"
)

// Logger writes timestamped log lines to a file.
type Logger struct {
	File   *os.File
	Writer *bufio.Writer
	Mu     sync.Mutex
	closed bool // new flag
}

// NewLogger creates or truncates the log file at the given path.
// The file is opened in append mode to support continuation across sessions.
func NewLogger(path string) (*Logger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logger open: %w", err)
	}
	return &Logger{
		File:   file,
		Writer: bufio.NewWriter(file),
	}, nil
}

// Info logs a message with INFO severity.
func (l *Logger) Info(msg string) {
	l.write("INFO", msg)
}

// Warn logs a message with WARN severity.
func (l *Logger) Warn(msg string) {
	l.write("WARN", msg)
}

// Error logs a message with ERROR severity.
func (l *Logger) Error(msg string) {
	l.write("ERROR", msg)
}

// write formats and writes a log line to the file.
func (l *Logger) write(level, msg string) {
	l.Mu.Lock()
	defer l.Mu.Unlock()

	now := time.Now().UTC()
	// Convert to float seconds with milliseconds precision
	epochSeconds := float64(now.UnixNano()) / 1e9
	timestamp := fmt.Sprintf("%.3f", epochSeconds)

	if l.closed {
		// fallback if closed: print to stderr
		fmt.Fprintf(os.Stderr, "[%s] [%s] %s\n", timestamp, level, msg)
		return
	}

	line := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, msg)

	if _, err := l.Writer.WriteString(line); err != nil {
		fmt.Fprintf(os.Stderr, "logger write failed: %v\n", err)
	}

	if err := l.Writer.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "logger flush failed: %v\n", err)
	}
}

// Close flushes and closes the log file.
func (l *Logger) Close() error {
	l.Mu.Lock()
	defer l.Mu.Unlock()

	if l.closed {
		return nil // already closed
	}
	l.closed = true

	if err := l.Writer.Flush(); err != nil {
		return fmt.Errorf("logger flush: %w", err)
	}
	if err := l.File.Close(); err != nil {
		return fmt.Errorf("logger close: %w", err)
	}
	return nil
}
