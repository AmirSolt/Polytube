//go:build windows

package recorder

import (
	"embed"
	"io"
	"os"
)

//go:embed assets/ffmpeg/ffmpeg.exe
var ffmpegBytes embed.FS

func LoadFFmpeg(ffmpegPath string) error {

	if _, err := os.Stat(ffmpegPath); err == nil {
		return nil
	}

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

	return nil
}
