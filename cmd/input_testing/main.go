package main

import (
	"context"
	"polytube/replay/internal/events"
	"polytube/replay/internal/input"
	"polytube/replay/internal/logger"
)

func main() {

	ctx, _ := context.WithCancel(context.Background())

	intLog := &logger.MockLogger{}
	evLog := &events.MockEventLogger{}

	// Input listener (keyboard/mouse/etc.).
	mnkinp := &input.MNKInputListener{
		EventLogger: evLog,
		Logger:      intLog,
	}

	ginp := &input.GamepadInputListener{
		EventLogger: evLog,
		Logger:      intLog,
	}
	go func() {

		intLog.Info("Input listener starting")
		mnkinp.Start(ctx)
		intLog.Info("Input listener stopped")
	}()

	go func() {
		intLog.Info("Input listener starting")
		ginp.Start(ctx)
		intLog.Info("Input listener stopped")
	}()

	select {}
}
