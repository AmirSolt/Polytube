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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"polytube/replay/internal/info"
	"polytube/replay/internal/logger"
	"polytube/replay/pkg/models"
	"polytube/replay/utils"
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
	SessionInfo         info.SessionInfo
}

// UploadTS scans DirPath for .ts files and uploads any that aren't yet uploaded.
// It skips files still being written by checking last-modified timestamps
// (simple heuristic: older than ~2s).
func (u *Uploader) UploadTS() {

	if u.DirPath == "" {
		u.Logger.Warn("uploader: no DirPath configured")
		return
	}
	// u.Logger.Info("uploader: scanning for .ts files")

	if u.ApiID == "" || u.ApiKey == "" {
		u.Logger.Error(fmt.Errorf("failed to upload: Api-ID or Api-Key are empty! Api-ID: %s, Api-Key: %s", u.ApiID, u.ApiKey).Error())
		return
	}
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
	if u.ApiID == "" || u.ApiKey == "" {
		u.Logger.Error(fmt.Errorf("failed to upload: Api-ID or Api-Key are empty! Api-ID: %s, Api-Key: %s", u.ApiID, u.ApiKey).Error())
		return
	}
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
	if u.ApiID == "" || u.ApiKey == "" {
		u.Logger.Error(fmt.Errorf("failed to upload: Api-ID or Api-Key are empty! Api-ID: %s, Api-Key: %s", u.ApiID, u.ApiKey).Error())
		return
	}
	u.WG.Add(1)
	go u.uploadFile(path)
}

// uploadFile coordinates getting the signed URL and uploading the file.
func (u *Uploader) uploadFile(path string) {
	defer u.WG.Done()

	fileName := filepath.Base(path)

	signedURL, err := u.getSignedURL(path)
	if err != nil {
		u.Logger.Error(fmt.Errorf("uploader: failed to get signed URL for %s: %w", fileName, err).Error())
		return
	}

	if err := u.putFileToSignedURL(signedURL, path); err != nil {
		u.Logger.Error(fmt.Errorf("uploader: failed to upload %s: %w", fileName, err).Error())
		return
	}

	u.markUploaded(path)
}

func (u *Uploader) CreateSession() (string, error) {
	url := fmt.Sprintf("%s/api/session/%s/%s",
		strings.TrimSuffix(u.EndpointURL, "/"),
		u.ApiID,     // maps to params.user_id
		u.SessionID, // maps to params.session_id
	)

	u.Logger.Info(fmt.Sprintf("Uploader: Creating session at %s", url))

	// u.SessionInfo to json
	sessionJSON, err := json.Marshal(u.SessionInfo)
	if err != nil {
		return url, fmt.Errorf("marshal SessionInfo: %w", err)
	}
	u.Logger.Info(fmt.Sprintf("Uploader: Creating session with json %s", sessionJSON))

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(sessionJSON))
	if err != nil {
		return url, fmt.Errorf("create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", u.ApiKey)

	client := u.client()
	resp, err := client.Do(req)
	if err != nil {
		return url, fmt.Errorf("POST request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return url, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	u.Logger.Info(fmt.Sprintf("Uploader: session created successfully at %s", url))
	return url, nil
}

// getSignedURL sends a GET request to retrieve a signed URL for uploading the given file.
func (u *Uploader) getSignedURL(path string) (string, error) {
	fileName := filepath.Base(path)
	contentLength, err := utils.GetFileContentLength(path)
	if err != nil {
		return "", err
	}

	params := []models.SearchParam{{
		Key:   "content_length",
		Value: fmt.Sprintf("%d", contentLength),
	}}

	url := fmt.Sprintf("%s/api/sign/%s/%s/%s/%s?%s",
		strings.TrimSuffix(u.EndpointURL, "/"),
		u.ApiID,     // maps to params.user_id
		u.SessionID, // maps to params.session_id
		fileName,    // maps to params.file_name
		"put",
		EncodeSearchParams(params),
	)

	u.Logger.Info(fmt.Sprintf("uploader: requesting signed URL for %s -> %s", fileName, url))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create GET request: %w", err)
	}
	req.Header.Set("api-key", u.ApiKey)

	client := u.client()
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	signedURL := strings.TrimSpace(string(body))
	u.Logger.Info(fmt.Sprintf("uploader: received signed URL for %s: %s", fileName, signedURL))
	return signedURL, nil
}

// putFileToSignedURL uploads the file to the signed URL via HTTP PUT.
func (u *Uploader) putFileToSignedURL(signedURL string, path string) error {
	fileName := filepath.Base(path)
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	req, err := http.NewRequest(http.MethodPut, signedURL, file)
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	client := u.client()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("PUT request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload failed (status %d): %s", resp.StatusCode, string(body))
	}

	u.Logger.Info(fmt.Sprintf("uploader: %s uploaded successfully! (status %d)", fileName, resp.StatusCode))
	return nil
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

// EncodeSearchParams builds a query string like "?gpu_brand=string string&tag=blue&tag=red"
func EncodeSearchParams(params []models.SearchParam) string {
	if len(params) == 0 {
		return ""
	}

	values := url.Values{}
	for _, p := range params {
		// Use Add instead of Set to allow repeated keys (for tags)
		values.Add(p.Key, p.Value)
	}

	return values.Encode()
}
