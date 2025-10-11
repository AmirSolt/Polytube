//go:build windows

// Package recorder starts and supervises an FFmpeg process that records a
// specific game window (by exact title) using the DXGI capture device on Windows.
// Output is written as HLS: a playlist.m3u8 manifest and segment files output_###.ts.
//
// Typical command (example):
//
//	ffmpeg -f dxgi -framerate 30 -i title="Window Title" \
//	  -vf scale=1280:720 -b:v 800k -b:a 128k -c:v libx264 -preset veryfast -crf 30 \
//	  -g 60 -pix_fmt yuv420p -f hls -hls_time 5 -hls_list_size 0 \
//	  -hls_segment_filename "C:\out\output_%03d.ts" "C:\out\playlist.m3u8"
package recorder

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"polytube/replay/internal/events"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"polytube/replay/utils"
)

// Recorder holds configuration for launching FFmpeg and waiting for it.
type Recorder struct {
	Title          string                 // exact window title to capture
	DirPath        string                 // directory to place HLS files
	FFmpegPath     string                 // path to ffmpeg.exe
	Logger         logger.LoggerInterface // internal logger for diagnostic output
	EventLogger    events.EventLoggerInterface
	cmd            *exec.Cmd
	stdioWG        sync.WaitGroup
	manifestPath   string
	segmentPattern string
	startOnce      sync.Once
	waitOnce       sync.Once
	startErr       error
	waitErr        error
}

// Start spawns ffmpeg.exe screen capture bound to the target window title.
// It wires stdout/stderr to the internal logger. If FFmpeg cannot be started, returns error.
//
// Notes:
//   - Ensures output directory exists.
//   - Verifies ffmpeg.exe path (attempts basic fallback lookup from PATH if empty).
//   - Uses HideWindow to avoid spawning a console window for FFmpeg.
func (r *Recorder) Start() error {
	r.startOnce.Do(func() {
		if r.Logger == nil {
			r.startErr = errors.New("recorder: Logger is required")
			return
		}
		if strings.TrimSpace(r.Title) == "" {
			r.startErr = errors.New("recorder: Title is required")
			return
		}
		if strings.TrimSpace(r.DirPath) == "" {
			r.startErr = errors.New("recorder: DirPath is required")
			return
		}

		// Ensure output directory exists.
		if err := os.MkdirAll(r.DirPath, 0o755); err != nil {
			r.startErr = fmt.Errorf("recorder: create out dir: %w", err)
			return
		}

		// Resolve ffmpeg path (allow using PATH if not set).
		ffmpeg, err := r.ensureFFmpegPath()
		if err != nil {
			r.startErr = err
			return
		}

		// Construct HLS target file paths.
		r.manifestPath = filepath.Join(r.DirPath, "playlist.m3u8")
		r.segmentPattern = filepath.Join(r.DirPath, "output_%03d.ts")

		// Build FFmpeg arguments.
		// Using conservative encoding defaults; tune as needed.
		args := []string{
			"-loglevel", "warning",
			"-y",

			// Capture video from a specific window (case-insensitive exact match)
			"-filter_complex", fmt.Sprintf(
				"gfxcapture=window_title='(?i)^%s$':max_framerate=30,hwdownload,format=bgra,scale=1280:720,format=yuv420p",
				r.Title,
			),

			// Disable audio completely
			"-an",

			// Encoding
			"-c:v", "libx264",
			"-preset", "veryfast",
			"-crf", "30",
			"-b:v", "700k",
			"-g", "60",

			// Output format (HLS)
			"-f", "hls",
			"-hls_time", "200",
			"-hls_list_size", "0",
			"-hls_segment_filename", r.segmentPattern,

			r.manifestPath,
		}
		r.Logger.Info(fmt.Sprintf("FFmpeg path: %s", ffmpeg))
		r.Logger.Info(fmt.Sprintf("FFmpeg args: %s", strings.Join(args, " ")))

		cmd := exec.Command(ffmpeg, args...)

		// Hide the child console window on Windows.
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			r.startErr = fmt.Errorf("recorder: stdout pipe: %w", err)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			r.startErr = fmt.Errorf("recorder: stderr pipe: %w", err)
			return
		}

		// Start process.
		if err := cmd.Start(); err != nil {
			r.startErr = fmt.Errorf("recorder: start ffmpeg: %w", err)
			return
		}
		r.cmd = cmd

		// Stream stdout/stderr to internal logger.
		r.stdioWG.Add(2)
		go r.pipeToLogger(stdout, "FFMPEG OUT", false)
		go r.pipeToLogger(stderr, "FFMPEG ERR", true)
	})

	return r.startErr
}

// Wait blocks until FFmpeg exits. It consumes the process state and logs the exit code.
// Returns an error if FFmpeg exits with a non-zero code or if Wait fails.
func (r *Recorder) Wait() error {
	r.waitOnce.Do(func() {
		if r.cmd == nil {
			r.waitErr = errors.New("recorder: Wait called before Start")
			return
		}
		// Wait for ffmpeg process.
		err := r.cmd.Wait()

		// Ensure stdout/stderr goroutines finish flushing logs.
		r.stdioWG.Wait()

		// Interpret exit status.
		if err != nil {
			// If possible, extract exit code.
			exitCode := extractExitCode(err)
			if exitCode >= 0 {
				r.waitErr = fmt.Errorf("ffmpeg exited with code %d: %w", exitCode, err)
			} else {
				r.waitErr = fmt.Errorf("ffmpeg wait error: %w", err)
			}
			if r.Logger != nil {
				r.Logger.Warn(r.waitErr.Error())
			}
			return
		}

		if r.Logger != nil {
			r.Logger.Info("FFmpeg process completed")
		}
	})

	return r.waitErr
}

func (r *Recorder) LogRecordingStartedEvent() error {
	event := models.Event{
		Timestamp:  utils.NowEpochSeconds(),
		EventType:  models.EventTypeRecordingStarted.String(),
		EventLevel: "",
		Content:    "",
		Value:      0,
	}
	r.EventLogger.LogEvent(event)
	return nil
}

// pipeToLogger scans a stream (stdout/stderr) line-by-line and forwards it to the internal logger.
// If isErr is true, lines are logged as WARN; otherwise as INFO.
func (r *Recorder) pipeToLogger(pipe ioReadCloser, prefix string, isErr bool) {
	defer r.stdioWG.Done()
	scanner := bufio.NewScanner(pipe)
	// increase buffer for long ffmpeg lines
	const maxLine = 512 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxLine)

	for scanner.Scan() {
		line := scanner.Text()
		if isErr {
			r.Logger.Warn(fmt.Sprintf("%s: %s", prefix, line))
		} else {
			r.Logger.Info(fmt.Sprintf("%s: %s", prefix, line))
		}
	}
	if err := scanner.Err(); err != nil {
		// Only best-effort since this is post-close territory
		r.Logger.Warn(fmt.Sprintf("%s reader error: %v", prefix, err))
	}
}

// ensureFFmpegPath validates or attempts to auto-resolve ffmpeg.exe.
// Priority:
//  1. r.FFmpegPath if set and exists
//  2. Find ffmpeg.exe via PATH
//  3. Attempt common install locations via registry (optional convenience)
func (r *Recorder) ensureFFmpegPath() (string, error) {
	// Use provided path if set and exists.
	if fp := strings.TrimSpace(r.FFmpegPath); fp != "" {
		if fileExists(fp) {
			return fp, nil
		}
	}

	// Look in PATH.
	if p, err := exec.LookPath("ffmpeg.exe"); err == nil && fileExists(p) {
		return p, nil
	}

	// Optional: Try to infer from registry if user installed via package managers.
	if p := lookupFFmpegFromRegistry(); p != "" && fileExists(p) {
		return p, nil
	}

	return "", fmt.Errorf("ffmpeg.exe not found; provide --ffmpeg or place ffmpeg.exe in PATH")
}

// lookupFFmpegFromRegistry tries to find ffmpeg installation paths via common package locations.
// Best-effort only; returns empty string on failure.
func lookupFFmpegFromRegistry() string {
	// Chocolatey often installs into C:\ProgramData\chocolatey\bin\ffmpeg.exe
	// Scoop often installs into %USERPROFILE%\scoop\apps\ffmpeg\current\bin\ffmpeg.exe
	// We can probe environment variables and registry for hints.

	// Scoop
	if home, err := os.UserHomeDir(); err == nil {
		scoopPath := filepath.Join(home, "scoop", "apps", "ffmpeg", "current", "bin", "ffmpeg.exe")
		if fileExists(scoopPath) {
			return scoopPath
		}
	}

	// Chocolatey PATH registration may already be covered by LookPath, but we can check default path:
	chocoPath := `C:\ProgramData\chocolatey\bin\ffmpeg.exe`
	if fileExists(chocoPath) {
		return chocoPath
	}

	// Try registry "App Paths"
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\ffmpeg.exe`, registry.QUERY_VALUE)
	if err == nil {
		defer k.Close()
		if v, _, err := k.GetStringValue(""); err == nil && v != "" && fileExists(v) {
			return v
		}
	}

	return ""
}

// extractExitCode attempts to get an exit code from exec.Cmd Wait error.
// Returns -1 if not available.
func extractExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// On Windows, ExitError.Sys() is syscall.WaitStatus
		if status, ok := ee.Sys().(windows.WaitStatus); ok {
			return int(status.ExitCode)
		}
	}
	return -1
}

// fileExists returns true if path exists and is a file.
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// --- Imports helpers ---

// Minimal interface to let pipeToLogger accept both stdout and stderr pipes without
// importing "io" at top-level twice in comments.
type ioReadCloser interface {
	Read(p []byte) (n int, err error)
	Close() error
}

// Sanity check (Windows-only build).
func init() {
	if runtime.GOOS != "windows" {
		panic("recorder is Windows-only (dxgi). Build tag should prevent this on non-Windows)")
	}
}
