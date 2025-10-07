package events

import (
	"fmt"
	"os"
	"sync"

	"polytube/replay/pkg/models"

	"github.com/apache/arrow/go/v16/arrow"
	"github.com/apache/arrow/go/v16/arrow/array"
	"github.com/apache/arrow/go/v16/arrow/ipc"
	"github.com/apache/arrow/go/v16/arrow/memory"
)

// ArrowEventLogger writes events as Arrow IPC stream with compression.
type ArrowEventLogger struct {
	mu      sync.Mutex
	file    *os.File
	writer  *ipc.Writer
	builder *array.RecordBuilder
	schema  *arrow.Schema
}

// NewArrowEventLogger creates a new Arrow IPC stream with schema and compression.
func NewArrowEventLogger(path string) (*ArrowEventLogger, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("arrow event logger create: %w", err)
	}

	pool := memory.NewGoAllocator()

	schema := arrow.NewSchema([]arrow.Field{
		{Name: "timestamp", Type: arrow.PrimitiveTypes.Float64},
		{Name: "eventType", Type: arrow.BinaryTypes.String},
		{Name: "eventLevel", Type: arrow.BinaryTypes.String},
		{Name: "content", Type: arrow.BinaryTypes.String},
		{Name: "value", Type: arrow.PrimitiveTypes.Float64},
	}, nil)

	writer := ipc.NewWriter(file,
		ipc.WithSchema(schema),
		ipc.WithZstd(), // change to ipc.WithLZ4() if you prefer faster compression
	)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("arrow event logger writer: %w", err)
	}

	builder := array.NewRecordBuilder(pool, schema)

	return &ArrowEventLogger{
		file:    file,
		writer:  writer,
		builder: builder,
		schema:  schema,
	}, nil
}

// LogEvent appends a single event as a one-row Record and writes it.
func (l *ArrowEventLogger) LogEvent(e models.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Append values
	l.builder.Field(0).(*array.Float64Builder).Append(e.Timestamp)
	l.builder.Field(1).(*array.StringBuilder).Append(e.EventType)
	l.builder.Field(2).(*array.StringBuilder).Append(e.EventLevel)
	l.builder.Field(3).(*array.StringBuilder).Append(e.Content)
	l.builder.Field(4).(*array.Float64Builder).Append(e.Value)

	// Build record
	record := l.builder.NewRecord()
	defer record.Release()

	// Reset builder for next event
	l.builder.Release()
	pool := memory.NewGoAllocator()
	l.builder = array.NewRecordBuilder(pool, l.schema)

	// Write record to stream
	if err := l.writer.Write(record); err != nil {
		return fmt.Errorf("arrow event logger write: %w", err)
	}

	// Ensure durability:
	// Flush OS buffers to disk to prevent data loss on crash
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("arrow event logger sync: %w", err)
	}

	return nil
}

// Close closes the writer and file.
func (l *ArrowEventLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.writer.Close(); err != nil {
		return fmt.Errorf("arrow event logger close writer: %w", err)
	}
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("arrow event logger close file: %w", err)
	}
	l.builder.Release()
	return nil
}
