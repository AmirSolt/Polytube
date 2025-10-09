package main

import (
	"polytube/replay/internal/input"
)

func main() {

	// ctx, _ := context.WithCancel(context.Background())

	// intLog := &logger.MockLogger{}
	// evLog := &events.MockEventLogger{}

	// // Input listener (keyboard/mouse/etc.).
	// inp := &input.InputListener{
	// 	EventLogger: evLog,
	// 	Logger:      intLog,
	// }
	// intLog.Info("Input listener starting")
	input.Start()
	// intLog.Info("Input listener stopped")

	select {}
}
