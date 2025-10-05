package input

import (
	"syscall"
	"unsafe"
)

var (
	xinput             = syscall.NewLazyDLL("xinput1_4.dll")
	procXInputGetState = xinput.NewProc("XInputGetState")
)

type XInputState struct {
	PacketNumber uint32
	Gamepad      XInputGamepad
}

type XInputGamepad struct {
	Buttons      uint16
	LeftTrigger  byte
	RightTrigger byte
	ThumbLX      int16
	ThumbLY      int16
	ThumbRX      int16
	ThumbRY      int16
}

// XInputGetState calls the Windows API directly.
func XInputGetState(index uint32) (*XInputState, error) {
	var state XInputState
	r, _, _ := procXInputGetState.Call(uintptr(index), uintptr(unsafe.Pointer(&state)))
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	return &state, nil
}
