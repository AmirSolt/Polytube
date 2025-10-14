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
	"polytube/replay/internal/info"
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
	PollSeconds int
	IsLoading   bool
	Tags        string
	AppName     string
	AppVersion  string
}

// serviceBundle groups all running components so main can manage their lifecycle.
type serviceBundle struct {
	ctx                  context.Context
	cancel               context.CancelFunc
	rec                  *recorder.Recorder
	upl                  *uploader.Uploader
	eventLogger          *events.ParquetEventLogger
	internalLogger       *logger.Logger
	mnkInputListener     *input.MNKInputListener
	gamepadInputListener *input.GamepadInputListener
	consoleListener      *console.ConsoleListener
}

// main parses flags, starts services, waits for FFmpeg to exit, and runs shutdown.
func main() {
	cfg := parseFlags()

	dataDir := filepath.Join(cfg.OutPath, "data")
	// Prepare file paths under the output folder.
	internalLogPath := filepath.Join(dataDir, "internal.log")
	eventsPath := filepath.Join(dataDir, "events.parquet")
	ffmpegPath := filepath.Join(cfg.OutPath, "ffmpeg.exe")

	if err := ensureDir(cfg.OutPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create out directory: %v\n", err)
		os.Exit(1)
	}
	if err := ensureAndWipeDir(dataDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create or wipe data directory: %v\n", err)
		os.Exit(1)
	}

	if err := recorder.LoadFFmpeg(ffmpegPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to load FFmpeg: %v\n", err)
		os.Exit(1)
	}

	if cfg.IsLoading {
		fmt.Println("Loading complete.")
		os.Exit(0)
	}

	// Initialize services and start background tasks.
	svcs, err := startServices(cfg, dataDir, internalLogPath, eventsPath, ffmpegPath)
	if err != nil {
		// Best-effort stderr message since internal logger may not have initialized.
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}

	// Record and block until FFmpeg exits (i.e., the game window closes).
	if err := svcs.rec.Start(); err != nil {
		svcs.internalLogger.Error(fmt.Errorf("recorder start failed: %w", err).Error())
		_ = shutdown(svcs) // attempt cleanup anyway
		os.Exit(1)
	}

	// Log event
	if err := svcs.rec.LogRecordingStartedEvent(); err != nil {
		svcs.internalLogger.Error(fmt.Errorf("failed to log RECORDING_STARTED event. Have to exit.: %w", err).Error())
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

	flag.BoolVar(&cfg.IsLoading, "load", false, "Loads nessesary binaries (ffmpeg) and exits. Ignores other flags.")
	flag.StringVar(&cfg.Title, "title", "", "Window title to record (exact match, use quotes if needed).")
	flag.StringVar(&cfg.OutPath, "out", "", "Directory where output files (video, logs, etc.) will be saved.")
	flag.StringVar(&cfg.Endpoint, "endpoint", "https://polytube.io", "Upload endpoint URL for cloud storage.")
	flag.StringVar(&cfg.ApiID, "api-id", "", "API ID for authentication when communicating with the upload endpoint.")
	flag.StringVar(&cfg.ApiKey, "api-key", "", "API Key for authentication when communicating with the upload endpoint.")
	flag.StringVar(&cfg.SessionID, "session-id", "", "*Leave Empty* Unique session identifier (UUID). Used to link uploads to an existing session on the server. Auto generated.")
	flag.StringVar(&cfg.Tags, "tags", "", "Comma-separated list of tags for organizing or categorizing the recording session (e.g., 'test,debug,build42').")
	flag.StringVar(&cfg.AppName, "app-name", "<Unassigned>", "Name of the app or game being recorded. Appears in analytics and upload metadata.")
	flag.StringVar(&cfg.AppVersion, "app-version", "<Unassigned>", "Version of the app being recorded. Use semantic versioning (e.g., '1.0.0').")
	flag.IntVar(&cfg.PollSeconds, "poll", defaultPollSeconds, fmt.Sprintf("Interval in seconds between uploader checks for new files to upload. Default: %d", defaultPollSeconds))
	flag.Parse()

	fmt.Printf("[DEBUG] Parsed flags: %+v\n", cfg)

	var missing []string
	if cfg.IsLoading {
		if cfg.OutPath == "" {
			missing = append(missing, "--out")
		}
	} else {
		if cfg.OutPath == "" {
			missing = append(missing, "--out")
		}
		if cfg.Title == "" {
			missing = append(missing, "--title")
		}
	}

	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing required flags: %v\n", missing)
		flag.Usage()
		os.Exit(2)
	}

	if cfg.SessionID == "" && !cfg.IsLoading {
		cfg.SessionID = uuid.New().String()
		fmt.Printf("Generated new session ID: %s\n", cfg.SessionID)
	}

	return cfg
}

// startServices initializes loggers, recorder, uploader, and background listeners/poller.
// It returns a service bundle with a cancellable context controlling all background work.
func startServices(cfg *cliConfig, dataDir, internalLogPath, eventsPath string, ffmpegPath string) (*serviceBundle, error) {
	// Internal logger first: everything else can log into it.
	intLog, err := logger.NewLogger(internalLogPath)
	if err != nil {
		return nil, fmt.Errorf("create internal logger: %w", err)
	}
	intLog.Info("Internal logger initialized")

	sessionInfo := info.SessionInfo{
		AppName:    &cfg.AppName,
		AppVersion: &cfg.AppVersion,
		Tags:       info.ParseTags(cfg.Tags),
		Logger:     intLog,
	}
	sessionInfo.PopulateDeviceInfo()
	intLog.Info(fmt.Sprintf("SessionInfo Populated: %+v", sessionInfo))

	// =====================
	// Log session ID
	intLog.Info(fmt.Sprintf("user inputs: %+v", cfg))

	// Structured event logger (ndjson).
	evLog, err := events.NewParquetEventLogger(eventsPath)
	if err != nil {
		intLog.Error(fmt.Sprintf("create event logger failed: %v", err))
		_ = intLog.Close()
		return nil, fmt.Errorf("create event logger: %w", err)
	}
	intLog.Info("Event logger initialized")

	// Recorder configured to write HLS into dataDir and log FFmpeg output to internal logger.
	rec := &recorder.Recorder{
		Title:       cfg.Title,
		DirPath:     dataDir,
		FFmpegPath:  ffmpegPath,
		Logger:      intLog,
		EventLogger: evLog,
	}

	// Uploader: maintains in-memory set of uploaded files; logs into internal logger.
	upl := &uploader.Uploader{
		DirPath:             dataDir,
		EndpointURL:         cfg.Endpoint,
		ApiID:               cfg.ApiID,
		ApiKey:              cfg.ApiKey,
		SessionID:           cfg.SessionID,
		UploadedFiles:       make(map[string]bool),
		Logger:              intLog,
		InternalLogFilePath: internalLogPath,
		SessionInfo:         sessionInfo,
	}
	intLog.Info("Uploader initialized")

	// Cancellable context controlling background tasks.
	ctx, cancel := context.WithCancel(context.Background())

	// Input listener (keyboard/mouse/etc.).
	mnkInputListener := &input.MNKInputListener{
		EventLogger: evLog,
		Logger:      intLog,
	}
	go func() {
		intLog.Info("Input listener starting")
		mnkInputListener.Start(ctx)
		intLog.Info("Input listener stopped")
	}()

	// Gamepad input listener.
	ginp := &input.GamepadInputListener{
		EventLogger: evLog,
		Logger:      intLog,
	}
	go func() {
		intLog.Info("Input listener starting")
		ginp.Start(ctx)
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
		if url, err := upl.CreateSession(); err != nil {
			upl.Logger.Error(fmt.Errorf("uploader: failed to create session at %s: %w", url, err).Error())
			return
		}
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
		ctx:                  ctx,
		cancel:               cancel,
		rec:                  rec,
		upl:                  upl,
		eventLogger:          evLog,
		internalLogger:       intLog,
		mnkInputListener:     mnkInputListener,
		gamepadInputListener: ginp,
		consoleListener:      con,
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
			svcs.internalLogger.Error(fmt.Errorf("close event logger failed: %w", err).Error())
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

func ensureAndWipeDir(path string) error {
	// Ensure directory exists
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("ensure dir: %w", err)
	}

	// Read all entries inside
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}

	// Remove each entry
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if err := os.RemoveAll(entryPath); err != nil {
			return fmt.Errorf("remove %s: %w", entryPath, err)
		}
	}

	return nil
}

// ensureDir creates the directory if it does not exist.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// wipeDir removes all contents (files and subdirectories) inside the directory.
// It does NOT delete the directory itself.
// func wipeDir(path string) error {
// 	entries, err := os.ReadDir(path)
// 	if err != nil {
// 		return fmt.Errorf("read dir: %w", err)
// 	}
// 	for _, entry := range entries {
// 		entryPath := filepath.Join(path, entry.Name())
// 		if err := os.RemoveAll(entryPath); err != nil {
// 			return fmt.Errorf("remove %s: %w", entryPath, err)
// 		}
// 	}
// 	return nil
// }
