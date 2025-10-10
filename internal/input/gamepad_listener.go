package input

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"time"

	"polytube/replay/internal/events"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"polytube/replay/utils"

	"github.com/go-gl/glfw/v3.3/glfw"
)

const POLL_INTERVAL_MS = 50
const ANALOG_THRESHOLD = 0.1

// --- InputListener ---
type GamepadInputListener struct {
	EventLogger events.EventLoggerInterface
	Logger      logger.LoggerInterface
	lastStates  map[string]float64
}

// Start begins listening for all input devices until context is canceled.
func (l *GamepadInputListener) Start(ctx context.Context) {
	if l.EventLogger == nil || l.Logger == nil {
		return
	}
	l.Logger.Info("input listener: starting GLFW joystick + keyboard + mouse")
	l.lastStates = make(map[string]float64)

	// GLFW must run on main OS thread
	runtime.LockOSThread()

	if err := glfw.Init(); err != nil {
		l.Logger.Warn(fmt.Sprintf("GLFW init failed: %v", err))
		return
	}
	defer glfw.Terminate()

	// Create hidden window for keyboard/mouse input
	glfw.WindowHint(glfw.Visible, glfw.False)
	window, err := glfw.CreateWindow(640, 480, "Input Listener", nil, nil)
	if err != nil {
		l.Logger.Warn(fmt.Sprintf("GLFW window creation failed: %v", err))
		return
	}
	window.MakeContextCurrent()

	// Sticky input ensures no missed presses
	window.SetInputMode(glfw.StickyKeysMode, glfw.True)
	window.SetInputMode(glfw.StickyMouseButtonsMode, glfw.True)

	l.Logger.Info("GLFW input callbacks installed")

	ticker := time.NewTicker(POLL_INTERVAL_MS * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.Logger.Info("input listener: stopping listeners")
			window.Destroy()
			return

		case <-ticker.C:
			glfw.PollEvents()
			l.pollJoysticks()
		}
	}
}

// --- Poll GLFW Joysticks ---
func (l *GamepadInputListener) pollJoysticks() {
	for jid := glfw.Joystick1; jid <= glfw.Joystick16; jid++ {
		if !jid.Present() {
			continue
		}

		axes := jid.GetAxes()
		buttons := jid.GetButtons()

		// Log axes
		for i, axis := range axes {
			name, ok := AxisNames[i]
			if !ok {
				name = fmt.Sprintf("Axis%d", i)
			}
			l.logEvent(models.EventLevelJoypad, name, float64(axis))
		}

		// Log button states
		for i, pressed := range buttons {
			value := 0.0
			if pressed == glfw.Press {
				value = 1.0
			}
			name, ok := ButtonNames[i]
			if !ok {
				name = fmt.Sprintf("Button%d", i)
			}
			l.logEvent(models.EventLevelJoypad, name, value)
		}
	}
}

// --- Deduplicated logging with thresholds ---
func (l *GamepadInputListener) logEvent(level models.EventLevel, key string, value float64) {
	if key == "" {
		return
	}
	id := level.String() + ":" + key
	prev, ok := l.lastStates[id]

	if ok {
		// Analog threshold
		if level == models.EventLevelJoypad && math.Abs(prev-value) < ANALOG_THRESHOLD {
			return
		}
		// Buttons/keyboard/mouse exact change only
		if prev == value {
			return
		}
	}

	l.lastStates[id] = value

	event := models.Event{
		Timestamp:  utils.NowEpochSeconds(),
		EventType:  models.EventTypeInputLog.String(),
		EventLevel: level.String(),
		Content:    key,
		Value:      value,
	}
	if err := l.EventLogger.LogEvent(event); err != nil {
		l.Logger.Warn(fmt.Sprintf("input listener: failed to log event: %v", err))
	}
}

var AxisNames = map[int]string{
	0: "LeftStickX",
	1: "LeftStickY",
	2: "RightStickX",
	3: "RightStickY",
	4: "LeftTrigger",
	5: "RightTrigger",
}

var ButtonNames = map[int]string{
	0:  "A",
	1:  "B",
	2:  "X",
	3:  "Y",
	4:  "LeftBumper",
	5:  "RightBumper",
	6:  "Back",
	7:  "Start",
	8:  "LeftStick",
	9:  "RightStick",
	10: "DpadUp",
	11: "DpadRight",
	12: "DpadDown",
	13: "DpadLeft",
	14: "Home",
}
