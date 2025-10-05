//go:build windows

package recorder

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets/ffmpeg/ffmpeg.exe
var ffmpegBytes embed.FS

// ExtractFFmpeg extracts the embedded ffmpeg.exe into a temp directory
// and returns the absolute path to the extracted file.
func ExtractFFmpeg() (string, error) {
	tempDir, err := os.MkdirTemp("", "replay_ffmpeg")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	outPath := filepath.Join(tempDir, "ffmpeg.exe")

	data, err := ffmpegBytes.ReadFile("assets/ffmpeg/ffmpeg.exe")
	if err != nil {
		return "", fmt.Errorf("read embedded ffmpeg: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0o755); err != nil {
		return "", fmt.Errorf("write ffmpeg file: %w", err)
	}

	return outPath, nil
}
