package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sickplanet/onvif2go/internal/onvif"
)

const (
	thumbnailDir = "data/thumbnails"
	snapshotDir  = "data/snapshots"
)

var snapshotHTTPClient = &http.Client{Timeout: 15 * time.Second}

// ensureThumbnailDir creates the necessary directories for thumbnails and snapshots
func ensureThumbnailDir() error {
	if err := os.MkdirAll(thumbnailDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(snapshotDir, 0o755)
}

// -----------------------------------------------------------------------------
// System Thumbnails (Public Page & Device List)
// -----------------------------------------------------------------------------

func thumbnailFilePath(deviceID string) string {
	return filepath.Join(thumbnailDir, sanitizeDeviceIDForPath(deviceID)+".jpg")
}

func (s *Server) thumbnailURL(deviceID string) string {
	path := thumbnailFilePath(deviceID)
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	v := info.ModTime().Unix()
	return fmt.Sprintf("/public/thumbnails/%s.jpg?v=%d", url.PathEscape(deviceID), v)
}

// saveThumbnailBytes saves the raw image data as the system thumbnail
func (s *Server) saveThumbnailBytes(deviceID string, data []byte) error {
	if err := ensureThumbnailDir(); err != nil {
		return err
	}
	path := thumbnailFilePath(deviceID)
	tempFile := path + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tempFile, path)
}

func (s *Server) loadThumbnailBytes(deviceID string) ([]byte, error) {
	path := thumbnailFilePath(deviceID)
	return os.ReadFile(path)
}

func (s *Server) deleteThumbnail(deviceID string) {
	path := thumbnailFilePath(deviceID)
	_ = os.Remove(path)
}

func renameThumbnailFile(oldID, newID string) error {
	oldPath := thumbnailFilePath(oldID)
	newPath := thumbnailFilePath(newID)
	if _, err := os.Stat(oldPath); err == nil {
		return os.Rename(oldPath, newPath)
	}
	return nil
}

func (s *Server) handlePublicThumbnail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/public/thumbnails/")
	if !strings.HasSuffix(path, ".jpg") {
		http.NotFound(w, r)
		return
	}
	deviceID := strings.TrimSuffix(path, ".jpg")

	// Basic validation
	if deviceID == "" || strings.Contains(deviceID, "/") || strings.Contains(deviceID, "\\") {
		http.NotFound(w, r)
		return
	}

	s.serveThumbnailFile(w, r, deviceID)
}

func (s *Server) serveThumbnailFile(w http.ResponseWriter, r *http.Request, deviceID string) {
	path := thumbnailFilePath(deviceID)
	info, err := os.Stat(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

// checkAndUpdateThumbnail updates the thumbnail if it's missing or older than 60 minutes.
// It attempts to fetch a snapshot via ONVIF.
func (s *Server) checkAndUpdateThumbnail(deviceID string) {
	path := thumbnailFilePath(deviceID)
	info, err := os.Stat(path)

	shouldUpdate := false
	if err != nil {
		if os.IsNotExist(err) {
			shouldUpdate = true
		}
	} else {
		if time.Since(info.ModTime()) > 60*time.Minute {
			shouldUpdate = true
		}
	}

	if shouldUpdate {
		go func() {
			if err := s.captureAndSaveThumbnail(deviceID); err != nil {
				// Log error but don't fail the request.
				// This is expected if the camera doesn't support ONVIF snapshots.
				// The client-side fallback (in app.js) should handle this by uploading a frame.
				log.Printf("[Thumbnail] Auto-update failed for %s: %v", deviceID, err)
			} else {
				log.Printf("[Thumbnail] Auto-updated for %s", deviceID)
			}
		}()
	}
}

func (s *Server) captureAndSaveThumbnail(deviceID string) error {
	device, ok := s.getDeviceSnapshot(deviceID)
	if !ok {
		return fmt.Errorf("device not found")
	}
	data, err := s.captureThumbnailFromCamera(device)
	if err != nil {
		return err
	}
	return s.saveThumbnailBytes(device.ID, data)
}

func (s *Server) captureThumbnailFromCamera(device *Device) ([]byte, error) {
	profileToken := ""
	if len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}
	raw, contentType, err := s.fetchSnapshotData(device, profileToken)
	if err != nil {
		return nil, err
	}
	jpegData, err := normalizeToJPEG(raw, contentType)
	if err != nil {
		return nil, err
	}
	return jpegData, nil
}

func (s *Server) fetchSnapshotData(device *Device, profileToken string) ([]byte, string, error) {
	if profileToken == "" {
		if len(device.Profiles) == 0 {
			return nil, "", fmt.Errorf("device has no media profiles")
		}
		profileToken = device.Profiles[0].Token
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)
	snapshotURI, err := client.GetSnapshotURI(profileToken)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequest(http.MethodGet, snapshotURI, nil)
	if err != nil {
		return nil, "", err
	}

	if device.Username != "" {
		req.SetBasicAuth(device.Username, device.Password)
	}

	resp, err := snapshotHTTPClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("snapshot request failed: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	return data, contentType, nil
}

func normalizeToJPEG(data []byte, contentType string) ([]byte, error) {
	if strings.Contains(contentType, "jpeg") || strings.Contains(contentType, "jpg") {
		return data, nil
	}

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	if format == "jpeg" {
		return data, nil
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// -----------------------------------------------------------------------------
// User Snapshots (Gallery)
// -----------------------------------------------------------------------------

// SnapshotFile represents a snapshot file in the gallery
type SnapshotFile struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Timestamp time.Time `json:"timestamp"`
	Size      int64     `json:"size"`
	User      string    `json:"user"`
}

func snapshotDirPath(deviceID string) string {
	return filepath.Join(snapshotDir, sanitizeDeviceIDForPath(deviceID))
}

func (s *Server) ensureDeviceSnapshotDir(deviceID string) error {
	return os.MkdirAll(snapshotDirPath(deviceID), 0o755)
}

func (s *Server) saveUserSnapshot(deviceID string, username string, data []byte) error {
	if err := s.ensureDeviceSnapshotDir(deviceID); err != nil {
		return err
	}

	// Format: UserName_UUID_DD_MM_YY_HH_MM_SS.jpeg
	timestamp := time.Now().Format("02_01_06_15_04_05")
	filename := fmt.Sprintf("%s_%s_%s.jpeg", sanitizeFilename(username), deviceID, timestamp)
	path := filepath.Join(snapshotDirPath(deviceID), filename)

	return os.WriteFile(path, data, 0o644)
}

func (s *Server) listUserSnapshots(deviceID string) ([]SnapshotFile, error) {
	dir := snapshotDirPath(deviceID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SnapshotFile{}, nil
		}
		return nil, err
	}

	var snapshots []SnapshotFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".jpeg") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Parse filename to get user
		parts := strings.Split(entry.Name(), "_")
		user := "unknown"
		if len(parts) > 0 {
			user = parts[0]
		}

		snapshots = append(snapshots, SnapshotFile{
			Name:      entry.Name(),
			Path:      fmt.Sprintf("/api/devices/%s/snapshots/%s", deviceID, entry.Name()),
			Timestamp: info.ModTime(),
			Size:      info.Size(),
			User:      user,
		})
	}
	return snapshots, nil
}

func (s *Server) deleteUserSnapshot(deviceID, filename string) error {
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return fmt.Errorf("invalid filename")
	}
	path := filepath.Join(snapshotDirPath(deviceID), filename)
	return os.Remove(path)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func sanitizeDeviceIDForPath(id string) string {
	safe := strings.ReplaceAll(id, "..", "")
	safe = strings.ReplaceAll(safe, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	safe = strings.ReplaceAll(safe, string(os.PathSeparator), "_")
	if safe == "" {
		safe = "device"
	}
	return safe
}

func sanitizeFilename(name string) string {
	safe := strings.ReplaceAll(name, "..", "")
	safe = strings.ReplaceAll(safe, "/", "")
	safe = strings.ReplaceAll(safe, "\\", "")
	return safe
}

func friendlySnapshotError(err error) string {
	if err == nil {
		return ""
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "actionnotsupported"):
		return "Camera does not support ONVIF snapshot requests."
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "not authorized") || strings.Contains(lower, "401"):
		return "Snapshot request was rejected by the camera. Check credentials or permissions."
	case strings.Contains(lower, "not found") || strings.Contains(lower, "404"):
		return "Snapshot endpoint on the camera returned not found."
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		return "Snapshot request to the camera timed out."
	}

	if len(msg) > 512 {
		return msg[:512] + "..."
	}
	return msg
}
