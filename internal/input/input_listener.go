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

// --- InputListener ---
type InputListener struct {
	EventLogger *events.EventLogger
	Logger      *logger.Logger
	lastStates  map[string]float64
}

// Start begins listening for all input devices until context is canceled.
func (l *InputListener) Start(ctx context.Context) {
	if l.EventLogger == nil || l.Logger == nil {
		return
	}
	l.Logger.Info("input listener: starting GLFW + XInput listeners")
	l.lastStates = make(map[string]float64)

	// GLFW must run on main OS thread
	runtime.LockOSThread()

	if err := glfw.Init(); err != nil {
		l.Logger.Warn(fmt.Sprintf("GLFW init failed: %v", err))
		return
	}
	defer glfw.Terminate()

	// --- Detect joysticks ---
	for jid := glfw.Joystick1; jid <= glfw.Joystick16; jid++ {
		if jid.Present() {
			name := jid.GetName()
			axes := jid.GetAxes()
			buttons := jid.GetButtons()
			isGamepad := jid.IsGamepad()
			l.Logger.Info(fmt.Sprintf("Joystick %d detected: %s | IsGamepad=%v | Axes=%d | Buttons=%d",
				jid, name, isGamepad, len(axes), len(buttons)))

			if len(axes) == 0 && len(buttons) == 0 {
				l.Logger.Warn(fmt.Sprintf("Joystick %d (%s): no axes/buttons detected — GLFW cannot read input", jid, name))
			}
		}
	}

	// Create hidden window for keyboard/mouse input
	glfw.WindowHint(glfw.Visible, glfw.False)
	window, err := glfw.CreateWindow(640, 480, "Input Listener", nil, nil)
	if err != nil {
		l.Logger.Warn(fmt.Sprintf("GLFW window creation failed: %v", err))
		return
	}
	window.MakeContextCurrent()

	window.SetKeyCallback(l.onKey)
	window.SetMouseButtonCallback(l.onMouseButton)

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
			l.pollGLFWJoysticks()
			l.pollXInputControllers()
		}
	}
}

// --- Keyboard callback ---
func (l *InputListener) onKey(w *glfw.Window, key glfw.Key, scancode int, action glfw.Action, mods glfw.ModifierKey) {
	keyName, ok := keyNames[key]
	if !ok {
		keyName = fmt.Sprintf("KEY_%d", key)
	}

	switch action {
	case glfw.Press:
		l.logEvent(models.EventLevelKeyboard, keyName, 1)
	case glfw.Release:
		l.logEvent(models.EventLevelKeyboard, keyName, 0)
	}
}

func (l *InputListener) onMouseButton(w *glfw.Window, button glfw.MouseButton, action glfw.Action, mods glfw.ModifierKey) {
	btnName, ok := mouseBtnNames[button]
	if !ok {
		btnName = fmt.Sprintf("MOUSE_%d", button)
	}

	switch action {
	case glfw.Press:
		l.logEvent(models.EventLevelMouse, btnName, 1)
	case glfw.Release:
		l.logEvent(models.EventLevelMouse, btnName, 0)
	}
}

// --- Poll GLFW joysticks (if supported) ---
func (l *InputListener) pollGLFWJoysticks() {
	for jid := glfw.Joystick1; jid <= glfw.Joystick16; jid++ {
		if !jid.Present() || !jid.IsGamepad() {
			continue
		}

		state := jid.GetGamepadState()
		if state == nil {
			continue
		}

		name := jid.GetGamepadName()
		if name == "" {
			name = fmt.Sprintf("Joystick_%d", jid)
		}

		for i, pressed := range state.Buttons {
			val := 0.0
			if pressed == glfw.Press {
				val = 1.0
			}
			key := fmt.Sprintf("%s_Button_%s", name, gamepadButtonName(i))
			l.logEvent(models.EventLevelJoypad, key, val)
		}
		for i, axis := range state.Axes {
			key := fmt.Sprintf("%s_Axis_%s", name, gamepadAxisName(i))
			l.logEvent(models.EventLevelJoypad, key, float64(axis))
		}
	}
}

// --- Poll Xbox controllers via XInput ---
func (l *InputListener) pollXInputControllers() {
	for i := uint32(0); i < 4; i++ {
		state, err := XInputGetState(i)
		if err != nil {
			continue // not connected
		}

		name := fmt.Sprintf("XInput_%d", i)
		g := state.Gamepad

		buttons := []struct {
			name string
			mask uint16
		}{
			{"DPad_Up", 0x0001},
			{"DPad_Down", 0x0002},
			{"DPad_Left", 0x0004},
			{"DPad_Right", 0x0008},
			{"Start", 0x0010},
			{"Back", 0x0020},
			{"LThumb", 0x0040},
			{"RThumb", 0x0080},
			{"LB", 0x0100},
			{"RB", 0x0200},
			{"A", 0x1000},
			{"B", 0x2000},
			{"X", 0x4000},
			{"Y", 0x8000},
		}
		for _, b := range buttons {
			val := 0.0
			if g.Buttons&b.mask != 0 {
				val = 1.0
			}
			key := fmt.Sprintf("%s_Button_%s", name, b.name)
			l.logEvent(models.EventLevelJoypad, key, val)
		}

		// Analog axes
		l.logEvent(models.EventLevelJoypad, name+"_Axis_LeftX", float64(g.ThumbLX)/32767.0)
		l.logEvent(models.EventLevelJoypad, name+"_Axis_LeftY", float64(g.ThumbLY)/32767.0)
		l.logEvent(models.EventLevelJoypad, name+"_Axis_RightX", float64(g.ThumbRX)/32767.0)
		l.logEvent(models.EventLevelJoypad, name+"_Axis_RightY", float64(g.ThumbRY)/32767.0)
		l.logEvent(models.EventLevelJoypad, name+"_Axis_LT", float64(g.LeftTrigger)/255.0)
		l.logEvent(models.EventLevelJoypad, name+"_Axis_RT", float64(g.RightTrigger)/255.0)
	}
}

// --- Deduplicated logging ---
func (l *InputListener) logEvent(level models.EventLevel, key string, value float64) {
	if key == "" {
		return
	}
	id := level.String() + ":" + key
	prev, ok := l.lastStates[id]

	// Apply thresholds:
	if ok {
		// Analog threshold: skip if change < 0.05
		if level == models.EventLevelJoypad && isAnalogKey(key) {
			if math.Abs(prev-value) < 0.05 {
				return
			}
		} else if prev == value {
			// For buttons or non-analogs, require exact change
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

// --- Human-readable mappings ---
func gamepadButtonName(index int) string {
	names := []string{
		"A", "B", "X", "Y",
		"LB", "RB", "Back", "Start",
		"Guide", "LThumb", "RThumb",
		"DPad_Up", "DPad_Right", "DPad_Down", "DPad_Left",
	}
	if index >= 0 && index < len(names) {
		return names[index]
	}
	return fmt.Sprintf("Unknown_%d", index)
}

func gamepadAxisName(index int) string {
	names := []string{
		"LeftX", "LeftY", "RightX", "RightY", "LT", "RT",
	}
	if index >= 0 && index < len(names) {
		return names[index]
	}
	return fmt.Sprintf("Unknown_%d", index)
}

func isAnalogKey(key string) bool {
	return key == "LeftX" || key == "LeftY" ||
		key == "RightX" || key == "RightY" ||
		key == "LT" || key == "RT"
}

var mouseBtnNames = map[glfw.MouseButton]string{
	glfw.MouseButtonLeft:   "LEFT_BUTTON",
	glfw.MouseButtonRight:  "RIGHT_BUTTON",
	glfw.MouseButtonMiddle: "MIDDLE_BUTTON",
}

var keyNames = map[glfw.Key]string{
	glfw.KeySpace:        "SPACE",
	glfw.KeyApostrophe:   "'",
	glfw.KeyComma:        ",",
	glfw.KeyMinus:        "-",
	glfw.KeyPeriod:       ".",
	glfw.KeySlash:        "/",
	glfw.Key0:            "0",
	glfw.Key1:            "1",
	glfw.Key2:            "2",
	glfw.Key3:            "3",
	glfw.Key4:            "4",
	glfw.Key5:            "5",
	glfw.Key6:            "6",
	glfw.Key7:            "7",
	glfw.Key8:            "8",
	glfw.Key9:            "9",
	glfw.KeySemicolon:    ";",
	glfw.KeyEqual:        "=",
	glfw.KeyA:            "A",
	glfw.KeyB:            "B",
	glfw.KeyC:            "C",
	glfw.KeyD:            "D",
	glfw.KeyE:            "E",
	glfw.KeyF:            "F",
	glfw.KeyG:            "G",
	glfw.KeyH:            "H",
	glfw.KeyI:            "I",
	glfw.KeyJ:            "J",
	glfw.KeyK:            "K",
	glfw.KeyL:            "L",
	glfw.KeyM:            "M",
	glfw.KeyN:            "N",
	glfw.KeyO:            "O",
	glfw.KeyP:            "P",
	glfw.KeyQ:            "Q",
	glfw.KeyR:            "R",
	glfw.KeyS:            "S",
	glfw.KeyT:            "T",
	glfw.KeyU:            "U",
	glfw.KeyV:            "V",
	glfw.KeyW:            "W",
	glfw.KeyX:            "X",
	glfw.KeyY:            "Y",
	glfw.KeyZ:            "Z",
	glfw.KeyEscape:       "ESCAPE",
	glfw.KeyEnter:        "ENTER",
	glfw.KeyTab:          "TAB",
	glfw.KeyBackspace:    "BACKSPACE",
	glfw.KeyInsert:       "INSERT",
	glfw.KeyDelete:       "DELETE",
	glfw.KeyRight:        "RIGHT",
	glfw.KeyLeft:         "LEFT",
	glfw.KeyDown:         "DOWN",
	glfw.KeyUp:           "UP",
	glfw.KeyPageUp:       "PAGE_UP",
	glfw.KeyPageDown:     "PAGE_DOWN",
	glfw.KeyHome:         "HOME",
	glfw.KeyEnd:          "END",
	glfw.KeyCapsLock:     "CAPS_LOCK",
	glfw.KeyScrollLock:   "SCROLL_LOCK",
	glfw.KeyNumLock:      "NUM_LOCK",
	glfw.KeyPrintScreen:  "PRINT_SCREEN",
	glfw.KeyPause:        "PAUSE",
	glfw.KeyF1:           "F1",
	glfw.KeyF2:           "F2",
	glfw.KeyF3:           "F3",
	glfw.KeyF4:           "F4",
	glfw.KeyF5:           "F5",
	glfw.KeyF6:           "F6",
	glfw.KeyF7:           "F7",
	glfw.KeyF8:           "F8",
	glfw.KeyF9:           "F9",
	glfw.KeyF10:          "F10",
	glfw.KeyF11:          "F11",
	glfw.KeyF12:          "F12",
	glfw.KeyLeftShift:    "LEFT_SHIFT",
	glfw.KeyLeftControl:  "LEFT_CTRL",
	glfw.KeyLeftAlt:      "LEFT_ALT",
	glfw.KeyRightShift:   "RIGHT_SHIFT",
	glfw.KeyRightControl: "RIGHT_CTRL",
	glfw.KeyRightAlt:     "RIGHT_ALT",
}
