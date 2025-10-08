package events

import (
	"fmt"
	"polytube/replay/pkg/models"
)

type MockEventLogger struct{}

func (l *MockEventLogger) LogEvent(e models.Event) error {
	fmt.Printf("[EVENT] %+v\n", e)
	return nil
}

func (l *MockEventLogger) Close() error {
	fmt.Println("[EVENT] logger closed")
	return nil
}
