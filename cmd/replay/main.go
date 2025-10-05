//go:build windows

// Package main provides the Windows-only CLI entrypoint for the Replay tool.
// It coordinates the lifecycle: parse flags -> init services -> start FFmpeg recording
// -> run background listeners/pollers -> wait for FFmpeg exit -> orderly shutdown.
//
// The program exits only after FFmpeg (recording the target window) exits, which
// happens when the target window closes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"polytube/replay/internal/console"
	"polytube/replay/internal/events"
	"polytube/replay/internal/input"
	"polytube/replay/internal/logger"
	"polytube/replay/internal/recorder"
	"polytube/replay/internal/uploader"
)

const (
	defaultPollSeconds = 5
)

// cliConfig captures all user-provided settings from flags.
type cliConfig struct {
	Title       string
	OutPath     string
	Endpoint    string
	ApiID       string
	ApiKey      string
	SessionID   string
	FFmpegPath  string
	PollSeconds int
}

// serviceBundle groups all running components so main can manage their lifecycle.
type serviceBundle struct {
	ctx             context.Context
	cancel          context.CancelFunc
	rec             *recorder.Recorder
	upl             *uploader.Uploader
	eventLogger     *events.EventLogger
	internalLogger  *logger.Logger
	inputListener   *input.InputListener
	consoleListener *console.ConsoleListener
}

// main parses flags, starts services, waits for FFmpeg to exit, and runs shutdown.
func main() {
	cfg := parseFlags()

	// Ensure output directory exists.
	if err := ensureDir(cfg.OutPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create out directory: %v\n", err)
		os.Exit(1)
	}
	if err := wipeDir(cfg.OutPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to wipe out directory: %v\n", err)
		os.Exit(1)
	}

	// Prepare file paths under the output folder.
	internalLogPath := filepath.Join(cfg.OutPath, "internal.log")
	eventsPath := filepath.Join(cfg.OutPath, "events.ndjson")

	// Initialize services and start background tasks.
	svcs, err := startServices(cfg, internalLogPath, eventsPath)
	if err != nil {
		// Best-effort stderr message since internal logger may not have initialized.
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}

	// Record and block until FFmpeg exits (i.e., the game window closes).
	if err := svcs.rec.Start(); err != nil {
		svcs.internalLogger.Error(fmt.Sprintf("recorder start failed: %v", err))
		_ = shutdown(svcs) // attempt cleanup anyway
		os.Exit(1)
	}
	svcs.internalLogger.Info("FFmpeg started; waiting for process to exit...")
	if err := svcs.rec.Wait(); err != nil {
		svcs.internalLogger.Warn(fmt.Sprintf("FFmpeg exited with error: %v", err))
	} else {
		svcs.internalLogger.Info("FFmpeg exited normally (window closed).")
	}

	// Execute the orderly shutdown sequence (strict order).
	if err := shutdown(svcs); err != nil {
		// We are at the end of the program; print to stderr in addition to logger.
		fmt.Fprintf(os.Stderr, "shutdown encountered errors: %v\n", err)
		// Do not os.Exit with non-zero here purely due to late-stage upload hiccups,
		// but you can choose to if your policy requires it.
	}
}

// parseFlags configures the CLI and validates required flags.
func parseFlags() *cliConfig {
	cfg := &cliConfig{}

	flag.StringVar(&cfg.Title, "title", "", "Window title to record (exact match)")
	flag.StringVar(&cfg.OutPath, "out", "", "Output directory for HLS segments and logs")
	flag.StringVar(&cfg.Endpoint, "endpoint", "https://www.polytube.io/api/sign", "Upload endpoint URL")
	flag.StringVar(&cfg.ApiID, "api-id", "", "API ID header value")
	flag.StringVar(&cfg.ApiKey, "api-key", "", "API Key header value")
	flag.StringVar(&cfg.SessionID, "session-id", uuid.New().String(), "Session id.")
	flag.StringVar(&cfg.FFmpegPath, "ffmpeg", "", "Path to ffmpeg.exe (optional; defaults to ./ffmpeg_bin/ffmpeg.exe relative to the executable)")
	flag.IntVar(&cfg.PollSeconds, "poll", defaultPollSeconds, "Uploader poll interval in seconds")
	flag.Parse()

	fmt.Printf("[DEBUG] Parsed flags: %+v\n", cfg)

	// Basic validation.
	missing := []string{}
	if cfg.Title == "" {
		missing = append(missing, "--title")
	}
	if cfg.OutPath == "" {
		missing = append(missing, "--out")
	}
	if cfg.Endpoint == "" {
		missing = append(missing, "--endpoint")
	}
	if cfg.ApiID == "" {
		missing = append(missing, "--api-id")
	}
	if cfg.ApiKey == "" {
		missing = append(missing, "--api-key")
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing required flags: %v\n", missing)
		flag.Usage()
		os.Exit(2)
	}

	return cfg
}

// startServices initializes loggers, recorder, uploader, and background listeners/poller.
// It returns a service bundle with a cancellable context controlling all background work.
func startServices(cfg *cliConfig, internalLogPath, eventsPath string) (*serviceBundle, error) {
	// Internal logger first: everything else can log into it.
	intLog, err := logger.NewLogger(internalLogPath)
	if err != nil {
		return nil, fmt.Errorf("create internal logger: %w", err)
	}
	intLog.Info("Internal logger initialized")

	// =====================
	// Log session ID
	intLog.Info(fmt.Sprintf("Session ID: %s", cfg.SessionID))
	// =====================
	// Set FFmpeg path
	if cfg.FFmpegPath == "" {
		ffmpegPath, err := recorder.ExtractFFmpeg()
		if err != nil {
			intLog.Error(fmt.Sprintf("failed to extract ffmpeg: %v", err))
			return nil, fmt.Errorf("failed to extract ffmpeg: %w", err)
		}
		cfg.FFmpegPath = ffmpegPath
	}
	// =====================

	// Structured event logger (ndjson).
	evLog, err := events.NewEventLogger(eventsPath)
	if err != nil {
		intLog.Error(fmt.Sprintf("create event logger failed: %v", err))
		_ = intLog.Close()
		return nil, fmt.Errorf("create event logger: %w", err)
	}
	intLog.Info("Event logger initialized")

	// Recorder configured to write HLS into cfg.OutPath and log FFmpeg output to internal logger.
	rec := &recorder.Recorder{
		Title:      cfg.Title,
		OutPath:    cfg.OutPath,
		FFmpegPath: cfg.FFmpegPath,
		Logger:     intLog,
	}

	// Uploader: maintains in-memory set of uploaded files; logs into internal logger.
	upl := &uploader.Uploader{
		DirPath:             cfg.OutPath,
		EndpointURL:         cfg.Endpoint,
		ApiID:               cfg.ApiID,
		ApiKey:              cfg.ApiKey,
		SessionID:           cfg.SessionID,
		UploadedFiles:       make(map[string]bool),
		Logger:              intLog,
		InternalLogFilePath: internalLogPath,
	}
	intLog.Info("Uploader initialized")

	// Cancellable context controlling background tasks.
	ctx, cancel := context.WithCancel(context.Background())

	// Input listener (keyboard/mouse/etc.).
	inp := &input.InputListener{
		EventLogger: evLog,
		Logger:      intLog,
	}
	go func() {
		intLog.Info("Input listener starting")
		inp.Start(ctx)
		intLog.Info("Input listener stopped")
	}()

	// Console listener (stdin lines => events).
	con := &console.ConsoleListener{
		EventLogger: evLog,
		Logger:      intLog,
	}
	go func() {
		intLog.Info("Console listener starting")
		con.Start(ctx)
		intLog.Info("Console listener stopped")
	}()

	// Uploader poller: periodically upload .ts segments as they appear.
	go func(poll int) {
		intLog.Info(fmt.Sprintf("Uploader poller starting (interval=%ds)", poll))
		ticker := time.NewTicker(time.Duration(poll) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				intLog.Info("Uploader poller stopping (context canceled)")
				return
			case <-ticker.C:
				upl.UploadTS()
			}
		}
	}(cfg.PollSeconds)

	return &serviceBundle{
		ctx:             ctx,
		cancel:          cancel,
		rec:             rec,
		upl:             upl,
		eventLogger:     evLog,
		internalLogger:  intLog,
		inputListener:   inp,
		consoleListener: con,
	}, nil
}

// shutdown executes the precise shutdown sequence:
//
// 1) Cancel background goroutines
// 2) Close event logger
// 3) Close internal logger
// 4) Upload remaining files (skip internal log)
// 5) Upload internal log last
// 6) Wait for all uploads to finish
func shutdown(svcs *serviceBundle) error {
	var firstErr error
	catch := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 1) stop background goroutines
	svcs.cancel()

	// 2) close event logger
	if err := svcs.eventLogger.Close(); err != nil {
		catch(fmt.Errorf("close event logger: %w", err))
		if svcs.internalLogger != nil {
			svcs.internalLogger.Error(fmt.Sprintf("close event logger failed: %v", err))
		}
	} else if svcs.internalLogger != nil {
		svcs.internalLogger.Info("Event logger closed")
	}

	// 3) upload all remaining non-log files
	if svcs.upl != nil {
		svcs.internalLogger.Info("Uploading remaining non-log files...")
		svcs.upl.UploadRemaining()
		// Wait for all non-log uploads to finish
		svcs.upl.WG.Wait()
		svcs.internalLogger.Info("All non-log uploads finished")
	}

	// 4) close internal logger AFTER all other uploads
	if svcs.internalLogger != nil {
		svcs.internalLogger.Info("Closing Internal Logger. *EXPECTED EXIT*")
		if err := svcs.internalLogger.Close(); err != nil {
			catch(fmt.Errorf("close internal logger: %w", err))
			// Cannot log after this point
		}
	}

	// 5) upload the internal log file last
	if svcs.upl != nil {
		svcs.upl.UploadLogFile()
		// Wait again for the log file upload to complete
		svcs.upl.WG.Wait()
	}

	return firstErr
}

// ensureDir creates the directory if it does not exist.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// wipeDir removes all contents (files and subdirectories) inside the directory.
// It does NOT delete the directory itself.
func wipeDir(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("remove %s: %w", entryPath, err)
		}
	}
	return nil
}
