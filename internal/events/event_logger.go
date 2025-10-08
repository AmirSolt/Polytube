package events

import (
	"fmt"
	"sync"

	"polytube/replay/pkg/models"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

// EventParquetRow defines the schema for each event in the parquet file.
type EventParquetRow struct {
	Timestamp  float64 `parquet:"name=timestamp, type=DOUBLE"`
	EventType  string  `parquet:"name=eventType, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	EventLevel string  `parquet:"name=eventLevel, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Content    string  `parquet:"name=content, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Value      float64 `parquet:"name=value, type=DOUBLE"`
}

// ParquetEventLogger writes events to a Parquet file.
type ParquetEventLogger struct {
	mu     sync.Mutex
	writer *writer.ParquetWriter
}

// NewParquetEventLogger creates a new Parquet writer with schema.
func NewParquetEventLogger(path string) (*ParquetEventLogger, error) {
	// Create file
	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		return nil, fmt.Errorf("create parquet file: %w", err)
	}

	// Create parquet writer
	pw, err := writer.NewParquetWriter(fw, new(EventParquetRow), 1)
	if err != nil {
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	// Optional: set compression codec
	pw.CompressionType = parquet.CompressionCodec_SNAPPY

	return &ParquetEventLogger{
		writer: pw,
	}, nil
}

// LogEvent appends one row to the parquet file.
func (l *ParquetEventLogger) LogEvent(e models.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	row := EventParquetRow{
		Timestamp:  e.Timestamp,
		EventType:  e.EventType,
		EventLevel: e.EventLevel,
		Content:    e.Content,
		Value:      e.Value,
	}

	if err := l.writer.Write(row); err != nil {
		return fmt.Errorf("parquet write: %w", err)
	}

	// Optional: Flush periodically if needed
	if l.writer.RowGroupSize >= 1000 {
		if err := l.writer.WriteStop(); err != nil {
			return fmt.Errorf("flush parquet: %w", err)
		}
	}

	return nil
}

// Close finalizes the file.
func (l *ParquetEventLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.writer.WriteStop(); err != nil {
		return fmt.Errorf("close parquet writer: %w", err)
	}

	if err := l.writer.PFile.Close(); err != nil {
		return fmt.Errorf("close parquet file: %w", err)
	}

	return nil
}
