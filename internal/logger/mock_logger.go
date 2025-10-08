package logger

import "fmt"

type MockLogger struct{}

func (l *MockLogger) Info(msg string)  { fmt.Println("[INFO]", msg) }
func (l *MockLogger) Warn(msg string)  { fmt.Println("[WARN]", msg) }
func (l *MockLogger) Error(msg string) { fmt.Println("[ERROR]", msg) }
