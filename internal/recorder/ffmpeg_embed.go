//go:build windows

package recorder

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

//go:embed assets/ffmpeg/ffmpeg.exe
var ffmpegBytes embed.FS

func ExtractFFmpeg() (string, error) {
	baseTemp := os.TempDir()
	fixedDir := filepath.Join(baseTemp, "___replay_ffmpeg_____")
	if err := os.MkdirAll(fixedDir, 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	outPath := filepath.Join(fixedDir, "ffmpeg.exe")
	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	}

	// Lower process priority temporarily
	setLowPriority()

	src, err := ffmpegBytes.Open("assets/ffmpeg/ffmpeg.exe")
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	// Stream copy instead of loading full file into memory
	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}

	// Restore normal priority
	setNormalPriority()

	return outPath, nil
}

func setLowPriority() {
	p := windows.CurrentProcess()
	windows.SetPriorityClass(p, windows.BELOW_NORMAL_PRIORITY_CLASS)
}
func setNormalPriority() {
	p := windows.CurrentProcess()
	windows.SetPriorityClass(p, windows.NORMAL_PRIORITY_CLASS)
}
