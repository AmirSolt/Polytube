// Package uploader manages uploading of generated files (HLS segments, manifests,
// event logs, internal logs) to a remote server.
//
// It provides three main entrypoints:
//   - UploadTS()          : periodically upload new .ts segment files.
//   - UploadRemaining()   : at shutdown, upload any remaining files except internal log.
//   - UploadLogFile()     : upload the internal log file last.
//
// Each upload is executed concurrently. Uploaded files are tracked in-memory only;
// no on-disk persistence is used.
//
// HTTP headers:
//
//	Api-Id: <ApiID>
//	Api-Key: <ApiKey>
//	Content-Type: application/octet-stream
package uploader

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"polytube/replay/internal/logger"
)

// Uploader manages background and shutdown uploads.
type Uploader struct {
	DirPath             string          // directory to scan
	EndpointURL         string          // base URL
	ApiID               string          // API ID header
	ApiKey              string          // API Key header
	SessionID           string          // Session ID
	UploadedFiles       map[string]bool // in-memory record of uploaded paths
	Client              *http.Client    // HTTP client (lazy-initialized)
	Mu                  sync.Mutex      // guards UploadedFiles
	WG                  sync.WaitGroup  // tracks concurrent uploads
	Logger              *logger.Logger  // internal logger
	InternalLogFilePath string
}

// UploadTS scans DirPath for .ts files and uploads any that aren't yet uploaded.
// It skips files still being written by checking last-modified timestamps
// (simple heuristic: older than ~2s).
func (u *Uploader) UploadTS() {
	if u.DirPath == "" {
		u.Logger.Warn("uploader: no DirPath configured")
		return
	}
	u.Logger.Info("uploader: scanning for .ts files")
	filepath.WalkDir(u.DirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			u.Logger.Warn(fmt.Sprintf("uploader: walk error: %v", err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".ts" {
			return nil
		}
		if u.isUploaded(path) {
			return nil
		}
		if !isStable(path) {
			// file still being written; skip for now
			return nil
		}
		u.Logger.Info(fmt.Sprintf("uploader: scheduling TS upload %s", path))
		u.WG.Add(1)
		go u.uploadFile(path)
		return nil
	})
}

// UploadRemaining scans all files in DirPath and uploads any not yet uploaded,
// except the internal log file (u.InternalLogFilePath).
func (u *Uploader) UploadRemaining() {
	u.Logger.Info("uploader: uploading remaining files (excluding internal log)")
	filepath.WalkDir(u.DirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			u.Logger.Warn(fmt.Sprintf("uploader: walk error: %v", err))
			return nil
		}
		if d.IsDir() {
			return nil
		}

		// Skip internal log file specifically
		if u.InternalLogFilePath != "" && filepath.Clean(path) == filepath.Clean(u.InternalLogFilePath) {
			u.Logger.Info(fmt.Sprintf("uploader: skipping internal log file %s", path))
			return nil
		}

		if u.isUploaded(path) {
			return nil
		}

		u.Logger.Info(fmt.Sprintf("uploader: scheduling upload %s", path))
		u.WG.Add(1)
		go u.uploadFile(path)
		return nil
	})
}

// UploadLogFile uploads the internal log file last, using u.InternalLogFilePath.
func (u *Uploader) UploadLogFile() {
	if u.InternalLogFilePath == "" {
		u.Logger.Warn("uploader: InternalLogFilePath not set, skipping log upload")
		return
	}
	path := u.InternalLogFilePath
	u.Logger.Info(fmt.Sprintf("uploader: scheduling internal log upload %s", path))
	u.WG.Add(1)
	go u.uploadFile(path)
}

// uploadFile sends the file via HTTP PUT to EndpointURL/<fileName>.
func (u *Uploader) uploadFile(path string) {
	defer u.WG.Done()

	fileName := filepath.Base(path)
	url := fmt.Sprintf("%s/%s/%s/%s",
		strings.TrimSuffix(u.EndpointURL, "/"),
		u.ApiID,     // maps to params.user_id
		u.SessionID, // maps to params.session_id
		fileName,    // maps to params.file_name
	)

	u.Logger.Info(fmt.Sprintf("uploader: uploading %s -> %s", fileName, url))

	file, err := os.Open(path)
	if err != nil {
		u.Logger.Warn(fmt.Sprintf("uploader: open %s failed: %v", path, err))
		return
	}
	defer file.Close()

	req, err := http.NewRequest(http.MethodPut, url, file)
	if err != nil {
		u.Logger.Warn(fmt.Sprintf("uploader: request %s failed: %v", fileName, err))
		return
	}

	// ✅ Set correct headers expected by the server
	req.Header.Set("secret-key", u.ApiKey) // matches server
	req.Header.Set("Content-Type", "application/octet-stream")

	client := u.client()
	resp, err := client.Do(req)
	if err != nil {
		u.Logger.Warn(fmt.Sprintf("uploader: http error for %s: %v", fileName, err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		u.markUploaded(path)
		u.Logger.Info(fmt.Sprintf("uploader: %s uploaded successfully! (status %d)", fileName, resp.StatusCode))
	} else {
		u.Logger.Error(fmt.Sprintf("uploader: upload failed: %s (status %d, response: %s)",
			fileName, resp.StatusCode, string(body)))
	}
}

// --- helpers ---

func (u *Uploader) client() *http.Client {
	if u.Client == nil {
		u.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return u.Client
}

func (u *Uploader) isUploaded(path string) bool {
	u.Mu.Lock()
	defer u.Mu.Unlock()
	return u.UploadedFiles[path]
}

func (u *Uploader) markUploaded(path string) {
	u.Mu.Lock()
	defer u.Mu.Unlock()
	u.UploadedFiles[path] = true
}

// isStable returns true if file’s mod time is at least 2s ago and size hasn’t changed
// since last check (simplified: just mod time check here).
func isStable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	age := time.Since(info.ModTime())
	return age > 2*time.Second
}
