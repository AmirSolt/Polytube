//go:build windows

package recorder

import (
	"embed"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

//go:embed assets/ffmpeg/ffmpeg.exe
var ffmpegBytes embed.FS

func LoadFFmpeg(ffmpegPath string) error {

	if _, err := os.Stat(ffmpegPath); err == nil {
		return nil
	}

	// Lower process priority temporarily
	setLowPriority()

	src, err := ffmpegBytes.Open("assets/ffmpeg/ffmpeg.exe")
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(ffmpegPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	// Stream copy instead of loading full file into memory
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}

	// Restore normal priority
	setNormalPriority()

	return nil
}

func setLowPriority() {
	p := windows.CurrentProcess()
	windows.SetPriorityClass(p, windows.BELOW_NORMAL_PRIORITY_CLASS)
}
func setNormalPriority() {
	p := windows.CurrentProcess()
	windows.SetPriorityClass(p, windows.NORMAL_PRIORITY_CLASS)
}
