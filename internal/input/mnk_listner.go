package input

import (
	"context"
	"fmt"
	"log"
	"polytube/replay/internal/events"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"polytube/replay/utils"
	"unsafe"

	"github.com/gonutz/w32/v3"
)

// // --- InputListener ---
type MNKInputListener struct {
	EventLogger events.EventLoggerInterface
	Logger      logger.LoggerInterface
}

func (l *MNKInputListener) Start(ctx context.Context) {
	log.SetFlags(0)

	hInst, err := w32.GetModuleHandle(nil)
	if err != nil {
		l.Logger.Error(fmt.Errorf("GetModuleHandle failed: %w", err).Error())
		return
	}

	// --- Keyboard hook (int32 in the signature!) ---
	kbProc := w32.NewHookProcedure(func(code int32, wParam, lParam uintptr) uintptr {
		if code >= 0 { // HC_ACTION == 0
			k := (*w32.KBDLLHOOKSTRUCT)(unsafe.Pointer(lParam)) // #nosec G103 safe Windows callback cast
			switch wParam {
			case w32.WM_KEYDOWN, w32.WM_SYSKEYDOWN:
				// log.Printf("[KEY DOWN] %s vk=0x%02X sc=0x%02X flags=0x%02X",
				// 	vkName(k.VkCode), k.VkCode, k.ScanCode, k.Flags)
				l.logEvent(getDevice(k.VkCode), vkName(k.VkCode), 1)
			case w32.WM_KEYUP, w32.WM_SYSKEYUP:
				// log.Printf("[KEY  UP ] %s vk=0x%02X sc=0x%02X flags=0x%02X",
				// 	vkName(k.VkCode), k.VkCode, k.ScanCode, k.Flags)
				l.logEvent(getDevice(k.VkCode), vkName(k.VkCode), 0)
			}
		}
		return w32.CallNextHookEx(0, code, wParam, lParam)
	})
	kbHook, err := w32.SetWindowsHookEx(w32.WH_KEYBOARD_LL, kbProc, hInst, 0)
	if err != nil {
		l.Logger.Error(fmt.Errorf("SetWindowsHookEx(WH_KEYBOARD_LL) failed: %w", err).Error())
		return
	}
	if kbHook == 0 {
		l.Logger.Error("SetWindowsHookEx(WH_KEYBOARD_LL) failed")
		return
	}
	defer w32.UnhookWindowsHookEx(kbHook)

	// --- Mouse hook (int32 in the signature!) ---
	msProc := w32.NewHookProcedure(func(code int32, wParam, lParam uintptr) uintptr {
		if code >= 0 {
			switch wParam {
			case w32.WM_LBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "VK_LBUTTON", 1)
			case w32.WM_LBUTTONUP:
				l.logEvent(models.EventLevelMouse, "VK_LBUTTON", 0)
			case w32.WM_RBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "VK_RBUTTON", 1)
			case w32.WM_RBUTTONUP:
				l.logEvent(models.EventLevelMouse, "VK_RBUTTON", 0)
			case w32.WM_MBUTTONDOWN:
				l.logEvent(models.EventLevelMouse, "VK_MBUTTON", 1)
			case w32.WM_MBUTTONUP:
				l.logEvent(models.EventLevelMouse, "VK_MBUTTON", 0)
				// Skip all others: move, wheel, xbuttons, etc.
			}
		}
		// Return immediately to avoid blocking cursor movement
		return w32.CallNextHookEx(0, code, wParam, lParam)
	})
	msHook, err := w32.SetWindowsHookEx(w32.WH_MOUSE_LL, msProc, hInst, 0)
	if err != nil {
		l.Logger.Error(fmt.Errorf("SetWindowsHookEx(WH_MOUSE_LL) failed: %w", err).Error())
		return
	}
	if msHook == 0 {
		l.Logger.Error("SetWindowsHookEx(WH_MOUSE_LL) failed")
		return
	}
	defer w32.UnhookWindowsHookEx(msHook)

	// --- Message loop ---
	done := make(chan struct{})
	go func() {
		var msg w32.MSG
		for {
			ret, err := w32.GetMessage(&msg, 0, 0, 0)
			if err != nil {
				l.Logger.Error(fmt.Errorf("GetMessage failed: %w", err).Error())
				break
			}
			if !ret {
				break
			}
			w32.TranslateMessage(&msg)
			w32.DispatchMessage(&msg)
		}
		close(done)
	}()

	// --- Wait for context cancellation ---
	select {
	case <-ctx.Done():
		l.Logger.Info("InputListener: stopping (context canceled)")
		w32.PostQuitMessage(0) // gracefully end message loop
	case <-done:
		l.Logger.Info("InputListener: message loop ended")
	}
}

// --- Deduplicated logging with thresholds ---
func (l *MNKInputListener) logEvent(level models.EventLevel, key string, value float64) {
	if key == "" {
		return
	}
	event := models.Event{
		Timestamp:  utils.NowEpochSeconds(),
		EventType:  models.EventTypeInputLog.String(),
		EventLevel: level.String(),
		Content:    key,
		Value:      value,
	}
	l.EventLogger.LogEvent(event)
}

var VKKbNames = map[uint32]string{
	// --- Control keys ---
	0x08: "VK_BACK",
	0x09: "VK_TAB",
	0x0D: "VK_RETURN",
	0x10: "VK_SHIFT",
	0x11: "VK_CONTROL",
	0x12: "VK_MENU", // Alt
	0x13: "VK_PAUSE",
	0x14: "VK_CAPITAL",
	0x1B: "VK_ESCAPE",
	0x20: "VK_SPACE",
	0x21: "VK_PRIOR", // PageUp
	0x22: "VK_NEXT",  // PageDown
	0x23: "VK_END",
	0x24: "VK_HOME",
	0x25: "VK_LEFT",
	0x26: "VK_UP",
	0x27: "VK_RIGHT",
	0x28: "VK_DOWN",
	0x2C: "VK_SNAPSHOT", // PrintScreen
	0x2D: "VK_INSERT",
	0x2E: "VK_DELETE",

	// --- Number keys ---
	0x30: "VK_0",
	0x31: "VK_1",
	0x32: "VK_2",
	0x33: "VK_3",
	0x34: "VK_4",
	0x35: "VK_5",
	0x36: "VK_6",
	0x37: "VK_7",
	0x38: "VK_8",
	0x39: "VK_9",

	// --- Alphabet keys ---
	0x41: "VK_A",
	0x42: "VK_B",
	0x43: "VK_C",
	0x44: "VK_D",
	0x45: "VK_E",
	0x46: "VK_F",
	0x47: "VK_G",
	0x48: "VK_H",
	0x49: "VK_I",
	0x4A: "VK_J",
	0x4B: "VK_K",
	0x4C: "VK_L",
	0x4D: "VK_M",
	0x4E: "VK_N",
	0x4F: "VK_O",
	0x50: "VK_P",
	0x51: "VK_Q",
	0x52: "VK_R",
	0x53: "VK_S",
	0x54: "VK_T",
	0x55: "VK_U",
	0x56: "VK_V",
	0x57: "VK_W",
	0x58: "VK_X",
	0x59: "VK_Y",
	0x5A: "VK_Z",

	// --- Function keys ---
	0x70: "VK_F1",
	0x71: "VK_F2",
	0x72: "VK_F3",
	0x73: "VK_F4",
	0x74: "VK_F5",
	0x75: "VK_F6",
	0x76: "VK_F7",
	0x77: "VK_F8",
	0x78: "VK_F9",
	0x79: "VK_F10",
	0x7A: "VK_F11",
	0x7B: "VK_F12",
}
var VKMouseNames = map[uint32]string{
	// --- Mouse buttons ---
	w32.WM_LBUTTONDOWN: "VK_LBUTTON",
	w32.WM_LBUTTONUP:   "VK_LBUTTON",
	w32.WM_RBUTTONDOWN: "VK_RBUTTON",
	w32.WM_RBUTTONUP:   "VK_RBUTTON",
	w32.WM_MBUTTONDOWN: "VK_MBUTTON",
	w32.WM_MBUTTONUP:   "VK_MBUTTON",
	w32.WM_XBUTTONDOWN: "VK_XBUTTON",
	w32.WM_XBUTTONUP:   "VK_XBUTTON",
}

// var VKGamepadNames = map[uint32]string{
// 	// Alphabet buttons
// 	0xC3: "VK_GAMEPAD_A",
// 	0xC4: "VK_GAMEPAD_B",
// 	0xC5: "VK_GAMEPAD_X",
// 	0xC6: "VK_GAMEPAD_Y",

// 	// shoulder and triggers
// 	0xC7: "VK_GAMEPAD_RIGHT_SHOULDER",
// 	0xC8: "VK_GAMEPAD_LEFT_SHOULDER",
// 	0xC9: "VK_GAMEPAD_LEFT_TRIGGER",
// 	0xCA: "VK_GAMEPAD_RIGHT_TRIGGER",

// 	// Dpad
// 	0xCB: "VK_GAMEPAD_DPAD_UP",
// 	0xCC: "VK_GAMEPAD_DPAD_DOWN",
// 	0xCD: "VK_GAMEPAD_DPAD_LEFT",
// 	0xCE: "VK_GAMEPAD_DPAD_RIGHT",

// 	// Center buttons
// 	0xCF: "VK_GAMEPAD_MENU", // Start
// 	0xD0: "VK_GAMEPAD_VIEW", // Back

// 	// left stick
// 	0xD3: "VK_GAMEPAD_LEFT_STICK_UP", // Press
// 	0xD4: "VK_GAMEPAD_LEFT_STICK_DOWN",
// 	0xD5: "VK_GAMEPAD_LEFT_STICK_LEFT",
// 	0xD6: "VK_GAMEPAD_LEFT_STICK_RIGHT",

// 	// Right stick
// 	0xD7: "VK_GAMEPAD_RIGHT_STICK_UP",
// 	0xD8: "VK_GAMEPAD_RIGHT_STICK_DOWN",
// 	0xD9: "VK_GAMEPAD_RIGHT_STICK_RIGHT",
// 	0xDA: "VK_GAMEPAD_RIGHT_STICK_LEFT",
// }

func vkName(vk uint32) string {
	if name, ok := VKKbNames[vk]; ok {
		return name
	}
	if name, ok := VKMouseNames[vk]; ok {
		return name
	}
	// if name, ok := VKGamepadNames[vk]; ok {
	// 	return name
	// }
	return fmt.Sprintf("0x%02X", vk)
}

func getDevice(vk uint32) models.EventLevel {
	if _, ok := VKKbNames[vk]; ok {
		return models.EventLevelKeyboard
	}
	if _, ok := VKMouseNames[vk]; ok {
		return models.EventLevelMouse
	}
	// if _, ok := VKGamepadNames[vk]; ok {
	// 	return models.EventLevelJoypad
	// }
	return models.EventLevelUknownDevice
}
