// Package console reads piped stdin lines and logs them as "console" events
// into the NDJSON event log.
//
// Example usage:
//
//	some_game.exe | replay.exe --title "Game" --out "C:\output" ...
//
// Example logged event:
//
//	{"type":"console","timestamp":"2025-10-04T15:00:00Z","payload":"Player joined"}
package console

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"polytube/replay/internal/events"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"polytube/replay/utils"
)

// ConsoleListener reads stdin lines and logs them as events.
type ConsoleListener struct {
	EventLogger *events.ArrowEventLogger
	Logger      *logger.Logger
}

// Start blocks and reads from stdin until the context is canceled.
// Each non-empty line becomes an event of type "console".
func (c *ConsoleListener) Start(ctx context.Context) {
	if c.EventLogger == nil || c.Logger == nil {
		return
	}

	c.Logger.Info("console listener: started reading from stdin")

	reader := bufio.NewReader(os.Stdin)

	for {
		select {
		case <-ctx.Done():
			c.Logger.Info("console listener: context canceled, stopping")
			return
		default:
			// Use non-blocking check to read line
			line, err := reader.ReadString('\n')
			if err != nil {
				// EOF or broken pipe — safe to stop
				if err.Error() == "EOF" {
					c.Logger.Info("console listener: stdin closed (EOF)")
				} else {
					c.Logger.Warn(fmt.Sprintf("console listener: read error: %v", err))
				}
				return
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			event := models.Event{
				Timestamp:  utils.NowEpochSeconds(),
				EventType:  models.EventTypeConsoleLog.String(),
				EventLevel: models.EventLevelLog.String(),
				Content:    line,
				Value:      0,
			}

			if err := c.EventLogger.LogEvent(event); err != nil {
				c.Logger.Warn(fmt.Sprintf("console listener: log event failed: %v", err))
			}
		}
	}
}
