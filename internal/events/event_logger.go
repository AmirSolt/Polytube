package events

import (
	"fmt"
	"time"

	"polytube/replay/pkg/models"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/source"
	"github.com/xitongsys/parquet-go/writer"
)

// EventLoggerInterface defines a basic event logger
type EventLoggerInterface interface {
	LogEvent(e models.Event)
	Close() error
}

// ParquetEventLogger uses a background goroutine and channel for non-blocking logging
type ParquetEventLogger struct {
	writer *writer.ParquetWriter
	file   source.ParquetFile

	ch   chan models.Event
	done chan struct{}
}

// NewParquetEventLogger creates a new buffered parquet event logger
func NewParquetEventLogger(path string) (*ParquetEventLogger, error) {
	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		return nil, fmt.Errorf("create parquet file: %w", err)
	}

	pw, err := writer.NewParquetWriter(fw, new(models.Event), 4)
	if err != nil {
		_ = fw.Close()
		return nil, fmt.Errorf("create parquet writer: %w", err)
	}

	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	pw.RowGroupSize = 128 * 1024 * 1024 // 128MB
	pw.PageSize = 8 * 1024              // 8KB

	l := &ParquetEventLogger{
		writer: pw,
		file:   fw,
		ch:     make(chan models.Event, 4096), // channel buffer size
		done:   make(chan struct{}),
	}

	go l.loop()
	return l, nil
}

// loop continuously writes events to the parquet writer and flushes periodically
func (l *ParquetEventLogger) loop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case e := <-l.ch:
			_ = l.writer.Write(e) // best-effort write, errors can be logged if needed

		case <-ticker.C:
			_ = l.writer.Flush(true)

		case <-l.done:
			// drain remaining events
			for {
				select {
				case e := <-l.ch:
					_ = l.writer.Write(e)
				default:
					_ = l.writer.Flush(true)
					_ = l.writer.WriteStop()
					_ = l.file.Close()
					return
				}
			}
		}
	}
}

// LogEvent enqueues an event non-blockingly
func (l *ParquetEventLogger) LogEvent(e models.Event) {
	select {
	case l.ch <- e:
	default:
		// channel full: drop event to avoid blocking
	}
}

// Close signals the writer goroutine to stop and flush remaining events
func (l *ParquetEventLogger) Close() error {
	close(l.done)
	return nil
}
