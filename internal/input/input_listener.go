//go:build windows

package input

import (
	"context"
	"fmt"
	"time"

	"github.com/0xcafed00d/joystick"
	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/mouse"
	"github.com/moutend/go-hook/pkg/types"

	"polytube/replay/internal/events"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
)

// Define missing mouse messages manually (from Win32 API)
const (
	WM_LBUTTONDOWN types.Message = 0x0201
	WM_LBUTTONUP   types.Message = 0x0202
	WM_RBUTTONDOWN types.Message = 0x0204
	WM_RBUTTONUP   types.Message = 0x0205
	WM_MBUTTONDOWN types.Message = 0x0207
	WM_MBUTTONUP   types.Message = 0x0208
)

const (
	pollInterval = 100 * time.Millisecond
	maxJoysticks = 16
)

// InputListener listens to keyboard, mouse, and joystick events.
type InputListener struct {
	EventLogger *events.EventLogger
	Logger      *logger.Logger

	lastStates map[string]float64 // map[device+button]value
}

// Start begins listening for all input devices until context is canceled.
func (l *InputListener) Start(ctx context.Context) {
	if l.EventLogger == nil || l.Logger == nil {
		return
	}
	l.Logger.Info("input listener: starting keyboard, mouse, and joystick listeners")
	l.lastStates = make(map[string]float64)

	// --- Keyboard listener ---
	go l.listenKeyboard(ctx)

	// --- Mouse listener ---
	go l.listenMouse(ctx)

	// --- Joysticks listener ---
	l.listenJoysticks(ctx)
}

func (l *InputListener) listenKeyboard(ctx context.Context) {
	keyboardChan := make(chan types.KeyboardEvent, 100)
	if err := keyboard.Install(nil, keyboardChan); err != nil {
		l.Logger.Warn(fmt.Sprintf("keyboard hook install failed: %v", err))
		return
	}
	defer keyboard.Uninstall()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-keyboardChan:
			switch ev.Message {
			case types.WM_KEYDOWN:
				l.logEvent(models.EventLevelKeyboard, vkToString(ev.VKCode), 1)
			case types.WM_KEYUP:
				l.logEvent(models.EventLevelKeyboard, vkToString(ev.VKCode), 0)
			}
		}
	}
}

func (l *InputListener) listenMouse(ctx context.Context) {
	mouseChan := make(chan types.MouseEvent, 100)
	if err := mouse.Install(nil, mouseChan); err != nil {
		l.Logger.Warn(fmt.Sprintf("mouse hook install failed: %v", err))
		return
	}
	defer mouse.Uninstall()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-mouseChan:
			switch ev.Message {
			case WM_LBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "LeftButton", 1)
			case WM_LBUTTONUP:
				l.logEvent(models.EventLevelMouse, "LeftButton", 0)
			case WM_RBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "RightButton", 1)
			case WM_RBUTTONUP:
				l.logEvent(models.EventLevelMouse, "RightButton", 0)
			case WM_MBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "MiddleButton", 1)
			case WM_MBUTTONUP:
				l.logEvent(models.EventLevelMouse, "MiddleButton", 0)
			}
		}
	}
}

func (l *InputListener) listenJoysticks(ctx context.Context) {
	type jsDev struct {
		id          int
		js          joystick.Joystick
		prevButtons uint32
		btnCount    int
	}
	var joysticks []jsDev
	for id := 0; id < maxJoysticks; id++ {
		if js, err := joystick.Open(id); err == nil {
			joysticks = append(joysticks, jsDev{
				id:       id,
				js:       js,
				btnCount: js.ButtonCount(),
			})
			l.Logger.Info(fmt.Sprintf("joystick %d connected: %s", id, js.Name()))
		}
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.Logger.Info("input listener: stopping joystick listeners")
			for _, d := range joysticks {
				d.js.Close()
			}
			return

		case <-ticker.C:
			for i := range joysticks {
				state, err := joysticks[i].js.Read()
				if err != nil {
					continue
				}
				prev := joysticks[i].prevButtons
				curr := state.Buttons
				pressed := curr &^ prev
				released := prev &^ curr
				if pressed != 0 || released != 0 {
					for b := 0; b < joysticks[i].btnCount && b < 32; b++ {
						mask := uint32(1) << uint(b)
						name := fmt.Sprintf("Button%d", b)
						if pressed&mask != 0 {
							l.logEvent(models.EventLevelJoypad, name, 1)
						}
						if released&mask != 0 {
							l.logEvent(models.EventLevelJoypad, name, 0)
						}
					}
					joysticks[i].prevButtons = curr
				}
			}
		}
	}
}

func (l *InputListener) logEvent(level models.EventLevel, button string, value float64) {
	if button == "" {
		return
	}
	key := level.String() + ":" + button
	if prev, ok := l.lastStates[key]; ok && prev == value {
		return // prevent spam if no state change
	}
	l.lastStates[key] = value

	event := models.Event{
		Timestamp:  time.Now().UTC(),
		EventType:  models.EventTypeInputLog.String(),
		EventLevel: level.String(),
		Content:    button,
		Value:      value, // 1 = pressed, 0 = released
	}
	if err := l.EventLogger.LogEvent(event); err != nil {
		l.Logger.Warn(fmt.Sprintf("input listener: failed to log event: %v", err))
	}
}

func vkToString(vk types.VKCode) string {
	if vk >= types.VK_A && vk <= types.VK_Z {
		return string(rune(vk - types.VK_A + 'A'))
	}
	if vk >= types.VK_0 && vk <= types.VK_9 {
		return string(rune(vk - types.VK_0 + '0'))
	}
	return fmt.Sprintf("VK_%d", vk)
}
