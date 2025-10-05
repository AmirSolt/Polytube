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
	// Use a fixed folder under the system temp dir
	baseTemp := os.TempDir() // e.g. C:\Users\<User>\AppData\Local\Temp
	fixedDir := filepath.Join(baseTemp, "___replay_ffmpeg_____")

	// Ensure directory exists
	if err := os.MkdirAll(fixedDir, 0o755); err != nil {
		return "", fmt.Errorf("create fixed dir: %w", err)
	}

	outPath := filepath.Join(fixedDir, "ffmpeg.exe")

	// Check if it already exists
	if _, err := os.Stat(outPath); err == nil {
		// File already exists — reuse
		return outPath, nil
	}

	// Otherwise, extract from embedded bytes
	data, err := ffmpegBytes.ReadFile("assets/ffmpeg/ffmpeg.exe")
	if err != nil {
		return "", fmt.Errorf("read embedded ffmpeg: %w", err)
	}

	if err := os.WriteFile(outPath, data, 0o755); err != nil {
		return "", fmt.Errorf("write ffmpeg file: %w", err)
	}

	return outPath, nil
}
