package events

import (
	"fmt"
	"sync"

	"polytube/replay/pkg/models"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type EventLoggerInterface interface {
	LogEvent(e models.Event) error
	Close() error
}

type ParquetEventLogger struct {
	mu     sync.Mutex
	writer *writer.ParquetWriter
}

// Create new parquet file writer
func NewParquetEventLogger(path string) (*ParquetEventLogger, error) {
	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		return nil, fmt.Errorf("create parquet file: %w", err)
	}

	pw, err := writer.NewParquetWriter(fw, new(models.Event), 4)
	if err != nil {
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	pw.RowGroupSize = 128 * 1024 * 1024 // 128MB
	pw.PageSize = 8 * 1024              // 8KB

	return &ParquetEventLogger{writer: pw}, nil
}

func (l *ParquetEventLogger) LogEvent(e models.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	row := models.Event{
		Timestamp:  e.Timestamp,
		EventType:  e.EventType,
		EventLevel: e.EventLevel,
		Content:    e.Content,
		Value:      e.Value,
	}
	if err := l.writer.Write(row); err != nil {
		return fmt.Errorf("parquet write: %w", err)
	}
	return nil
}

// Close flushes and finalizes the file
func (l *ParquetEventLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.writer == nil {
		return nil
	}

	if err := l.writer.WriteStop(); err != nil {
		return fmt.Errorf("close parquet writer: %w", err)
	}

	if err := l.writer.PFile.Close(); err != nil {
		return fmt.Errorf("close parquet file: %w", err)
	}

	l.writer = nil
	return nil
}
