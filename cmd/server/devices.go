package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sickplanet/onvif2go/internal/onvif"
	"github.com/sickplanet/onvif2go/internal/webrtc"
)

// DeviceConfigRequest represents a device configuration update
type DeviceConfigRequest struct {
	Username             *string `json:"username,omitempty"`
	Password             *string `json:"password,omitempty"`
	IsPublic             *bool   `json:"isPublic,omitempty"`
	AllowPublicPTZ       *bool   `json:"allowPublicPTZ,omitempty"`
	EventsEnabled        *bool   `json:"eventsEnabled,omitempty"`
	EventIntervalSeconds *int    `json:"eventIntervalSeconds,omitempty"`
}

// PublicProfileInfo represents a public camera profile
type PublicProfileInfo struct {
	Token string `json:"token"`
	Name  string `json:"name"`
}

// PublicCameraInfo represents public camera information
type PublicCameraInfo struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	ThumbnailURL string              `json:"thumbnailUrl"`
	AllowPTZ     bool                `json:"allowPtz"`
	Profiles     []PublicProfileInfo `json:"profiles,omitempty"`
}

// AddDeviceRequest represents a request to add a device
type AddDeviceRequest struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// TestCredentialsRequest represents a credential test request
type TestCredentialsRequest struct {
	Endpoint string `json:"endpoint"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// TestCredentialsResponse represents the result of a credential test
type TestCredentialsResponse struct {
	Success bool              `json:"success"`
	Message string            `json:"message"`
	Info    *onvif.DeviceInfo `json:"info,omitempty"`
}

// StreamURIResponse represents a stream URI response
type StreamURIResponse struct {
	StreamURI   string `json:"streamUri"`
	SnapshotURI string `json:"snapshotUri,omitempty"`
}

// WebRTCOfferRequest represents a WebRTC offer request
type WebRTCOfferRequest struct {
	Offer        string `json:"offer"`
	ProfileToken string `json:"profileToken"`
}

// WebRTCAnswerResponse represents a WebRTC answer response
type WebRTCAnswerResponse struct {
	Answer string `json:"answer"`
}

// handleDeviceConfig handles device configuration updates
func (s *Server) handleDeviceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]

	s.devicesMu.Lock()
	device, exists := s.devices[deviceID]
	if !exists {
		s.devicesMu.Unlock()
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	var req DeviceConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.devicesMu.Unlock()
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Username != nil {
		device.Username = *req.Username
	}
	if req.Password != nil {
		device.Password = *req.Password
	}

	user := s.getCurrentUser(r)
	if user != nil && user.IsAdmin {
		if req.IsPublic != nil {
			device.IsPublic = *req.IsPublic
		}
		if req.AllowPublicPTZ != nil {
			device.AllowPublicPTZ = *req.AllowPublicPTZ
		}
		if req.EventsEnabled != nil {
			if device.SupportsEvents {
				device.EventsEnabled = *req.EventsEnabled
			} else {
				device.EventsEnabled = false
			}
		}
		if req.EventIntervalSeconds != nil {
			interval := *req.EventIntervalSeconds
			if interval < 1 {
				interval = 1
			}
			device.EventIntervalSeconds = interval
		}
	}
	if device.EventIntervalSeconds < 1 {
		device.EventIntervalSeconds = 1
	}

	s.devicesMu.Unlock()

	if err := s.saveDevice(device); err != nil {
		log.Printf("failed to persist config for %s: %v", device.Name, err)
		writeError(w, http.StatusInternalServerError, "Failed to save configuration")
		return
	}

	s.updateEventWorker(deviceID)

	writeJSON(w, http.StatusOK, device)
}

// handlePublicCameras returns list of public cameras
func (s *Server) handlePublicCameras(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	s.devicesMu.RLock()
	cameras := make([]PublicCameraInfo, 0)
	for _, device := range s.devices {
		if device.IsPublic {
			thumbnailURL := s.thumbnailURL(device.ID)
			cameras = append(cameras, PublicCameraInfo{
				ID:           device.ID,
				Name:         device.Name,
				ThumbnailURL: thumbnailURL,
				AllowPTZ:     device.AllowPublicPTZ,
			})
		}
	}
	s.devicesMu.RUnlock()

	writeJSON(w, http.StatusOK, cameras)
}

// handlePublicCamera returns a single public camera
func (s *Server) handlePublicCamera(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/public/cameras/")

	s.devicesMu.RLock()
	device, exists := s.devices[id]
	s.devicesMu.RUnlock()

	if !exists || !device.IsPublic {
		writeError(w, http.StatusNotFound, "Camera not found")
		return
	}

	thumbnailURL := s.thumbnailURL(device.ID)
	profiles := make([]PublicProfileInfo, 0, len(device.Profiles))
	for _, p := range device.Profiles {
		profiles = append(profiles, PublicProfileInfo{
			Token: p.Token,
			Name:  p.Name,
		})
	}

	writeJSON(w, http.StatusOK, PublicCameraInfo{
		ID:           device.ID,
		Name:         device.Name,
		ThumbnailURL: thumbnailURL,
		AllowPTZ:     device.AllowPublicPTZ,
		Profiles:     profiles,
	})
}

// handleDiscover handles device discovery requests
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	timeout := 5 * time.Second
	if t := r.URL.Query().Get("timeout"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	devices, err := onvif.Discover(timeout)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, devices)
}

// handleAddDevice handles adding a new device
func (s *Server) handleAddDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req AddDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "Endpoint is required")
		return
	}

	if err := onvif.ValidateEndpoint(req.Endpoint); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Check for existing device with same endpoint
	s.devicesMu.RLock()
	for _, dev := range s.devices {
		if dev.Endpoint == req.Endpoint {
			s.devicesMu.RUnlock()
			writeError(w, http.StatusConflict, "Device with this endpoint already exists")
			return
		}
	}
	s.devicesMu.RUnlock()

	client := onvif.NewClient(req.Endpoint, req.Username, req.Password)

	info, err := client.GetDeviceInformation()
	if err != nil {
		log.Printf("Warning: Could not get device info: %v", err)
	}

	// Generate ID
	var id string
	if info != nil && info.SerialNumber != "" {
		// If SerialNumber looks like a UUID, use it
		if len(info.SerialNumber) == 36 && strings.Count(info.SerialNumber, "-") == 4 {
			id = info.SerialNumber
		}
	}
	if id == "" {
		id = generateUUID()
	}

	profiles, err := client.GetProfiles()
	if err != nil {
		log.Printf("Warning: Could not get profiles: %v", err)
	}

	capabilities, err := client.GetCapabilities()
	if err != nil {
		log.Printf("Warning: Could not get capabilities: %v", err)
	}
	supportsEvents := false
	eventService := ""
	if capabilities != nil && capabilities.EventsAddr != "" {
		supportsEvents = true
		eventService = capabilities.EventsAddr
	}

	name := req.Name
	if name == "" {
		if info != nil && info.Model != "" {
			name = info.Manufacturer + " " + info.Model
		} else {
			name = "ONVIF Device"
		}
	}

	device := &Device{
		ID:                   id,
		Name:                 name,
		Endpoint:             req.Endpoint,
		Username:             req.Username,
		Password:             req.Password,
		Info:                 info,
		Profiles:             profiles,
		Connected:            info != nil,
		LastSeen:             time.Now(),
		SupportsEvents:       supportsEvents,
		EventsEnabled:        false,
		EventIntervalSeconds: 5,
		eventService:         eventService,
	}

	s.devicesMu.Lock()
	s.devices[id] = device
	s.devicesMu.Unlock()

	if err := s.saveDevice(device); err != nil {
		log.Printf("failed to persist device %s: %v", device.Name, err)
		s.devicesMu.Lock()
		delete(s.devices, id)
		s.devicesMu.Unlock()
		writeError(w, http.StatusInternalServerError, "Failed to save device")
		return
	}

	writeJSON(w, http.StatusCreated, device)
}

// handleTestCredentials verifies a device endpoint with provided credentials
func (s *Server) handleTestCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req TestCredentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if err := onvif.ValidateEndpoint(req.Endpoint); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	client := onvif.NewClient(req.Endpoint, req.Username, req.Password)

	info, err := client.GetDeviceInformation()
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Failed to authenticate with device: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, TestCredentialsResponse{
		Success: true,
		Message: "Connection successful",
		Info:    info,
	})
}

// handleListDevices returns all devices
func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := s.getCurrentUser(r)

	s.devicesMu.RLock()
	devices := make([]*Device, 0, len(s.devices))
	for _, d := range s.devices {
		if user != nil && (user.IsAdmin || s.userHasCameraAccess(user, d.ID)) {
			devices = append(devices, d)
		}
	}
	s.devicesMu.RUnlock()

	writeJSON(w, http.StatusOK, devices)
}

// handleGetDevice returns a single device
func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
	id = strings.Split(id, "/")[0]

	s.devicesMu.RLock()
	device, exists := s.devices[id]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	writeJSON(w, http.StatusOK, device)
}

// handleDeleteDevice removes a device
func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/devices/")

	s.devicesMu.Lock()
	device, exists := s.devices[id]
	if !exists {
		s.devicesMu.Unlock()
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}
	delete(s.devices, id)
	s.devicesMu.Unlock()

	if err := s.deleteDeviceRecord(id); err != nil {
		log.Printf("failed to delete device %s from store: %v", id, err)
		s.devicesMu.Lock()
		s.devices[id] = device
		s.devicesMu.Unlock()
		writeError(w, http.StatusInternalServerError, "Failed to delete device")
		return
	}

	if s.eventManager != nil {
		s.eventManager.Disable(id)
	}
	s.streamManager.RemoveBridge(id)

	w.WriteHeader(http.StatusNoContent)
}

// handleGetStreamURI returns the stream URI for a device profile
func (s *Server) handleGetStreamURI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 3 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]
	profileToken := parts[2]

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	streamURI, err := client.GetStreamURI(profileToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	streamURI, _ = onvif.InjectCredentials(streamURI, device.Username, device.Password)

	snapshotURI, _ := client.GetSnapshotURI(profileToken)

	writeJSON(w, http.StatusOK, StreamURIResponse{
		StreamURI:   streamURI,
		SnapshotURI: snapshotURI,
	})
}

// handleWebRTCStream handles WebRTC streaming requests
func (s *Server) handleWebRTCStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	var req WebRTCOfferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Offer == "" {
		writeError(w, http.StatusBadRequest, "Offer is required")
		return
	}

	profileToken := req.ProfileToken
	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	if profileToken == "" {
		writeError(w, http.StatusBadRequest, "Profile token is required")
		return
	}

	// Use local RTSP proxy to multiplex connections
	rtspPort := "8554"
	if s.rtspService != nil && s.rtspService.server.RTSPAddress != "" {
		_, port, err := net.SplitHostPort(s.rtspService.server.RTSPAddress)
		if err == nil {
			rtspPort = port
		}
	}

	token := ""
	if s.rtspService != nil {
		token = s.rtspService.internalToken
	}

	// Construct proxy URL
	streamURI := fmt.Sprintf("rtsp://localhost:%s/%s/%s?token=%s",
		rtspPort, deviceID, url.PathEscape(profileToken), token)

	// Use empty credentials as the proxy handles authentication with the camera
	username := ""
	password := ""

	_, answer, err := webrtc.CreateAnswerHandler(req.Offer, streamURI, username, password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Trigger auto-thumbnail update
	s.checkAndUpdateThumbnail(deviceID)

	writeJSON(w, http.StatusOK, WebRTCAnswerResponse{Answer: answer})
}

// handleUploadThumbnail handles uploading a new thumbnail for the device
func (s *Server) handleUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 { // /api/devices/{id}/thumbnail
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	deviceID := parts[0]

	// Check access
	user := s.getCurrentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if !s.userHasCameraAccess(user, deviceID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	var req struct {
		Image string `json:"image"` // Base64 encoded image
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Image == "" {
		writeError(w, http.StatusBadRequest, "Image is required")
		return
	}

	// Remove data:image/jpeg;base64, prefix if present
	b64 := req.Image
	if idx := strings.Index(b64, ","); idx != -1 {
		b64 = b64[idx+1:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid base64 image")
		return
	}

	if err := s.saveThumbnailBytes(deviceID, data); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save thumbnail: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleDeviceEventsStream streams ONVIF events via Server-Sent Events
func (s *Server) handleDeviceEventsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 3 || parts[2] != "stream" {
		writeError(w, http.StatusBadRequest, "Invalid event stream path")
		return
	}

	deviceID := parts[0]
	device, ok := s.checkDeviceAccess(r, deviceID)
	if !ok {
		user := s.getCurrentUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
		} else {
			writeError(w, http.StatusForbidden, "Access denied")
		}
		return
	}

	if !device.SupportsEvents {
		writeError(w, http.StatusBadRequest, "Device does not support events")
		return
	}
	if !device.EventsEnabled {
		writeError(w, http.StatusBadRequest, "Events are not enabled for this device")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	s.updateEventWorker(deviceID)
	ch, err := s.eventManager.Subscribe(deviceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Events are not available")
		return
	}
	defer s.eventManager.Unsubscribe(deviceID, ch)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// handleProxySnapshot proxies a snapshot from the camera
func (s *Server) handleProxySnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 3 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]
	profileToken := parts[2]

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	snapshotURI, err := client.GetSnapshotURI(profileToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	u, err := url.Parse(snapshotURI)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if device.Username != "" && device.Password != "" {
		u.User = url.UserPassword(device.Username, device.Password)
	}

	resp, err := http.Get(u.String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	io.Copy(w, resp.Body)
}

// handleRefreshDevice refreshes device info and profiles
func (s *Server) handleRefreshDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	if !exists {
		s.devicesMu.RUnlock()
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}
	endpoint := device.Endpoint
	username := device.Username
	password := device.Password
	prevSupportsEvents := device.SupportsEvents
	prevEventService := device.eventService
	s.devicesMu.RUnlock()

	client := onvif.NewClient(endpoint, username, password)

	info, err := client.GetDeviceInformation()
	if err != nil {
		s.persistRefreshFailure(deviceID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	profiles, err := client.GetProfiles()
	if err != nil {
		s.persistRefreshFailure(deviceID)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	capabilities, err := client.GetCapabilities()
	if err != nil {
		log.Printf("Warning: Could not refresh capabilities for %s: %v", deviceID, err)
	}
	supportsEvents := prevSupportsEvents
	eventService := prevEventService
	if capabilities != nil {
		supportsEvents = capabilities.EventsAddr != ""
		eventService = capabilities.EventsAddr
	}

	updatedDevice, ok := s.mutateDevice(deviceID, func(d *Device) {
		d.Info = info
		d.Profiles = profiles
		d.Connected = true
		d.SupportsEvents = supportsEvents
		if !supportsEvents {
			d.EventsEnabled = false
		}
		d.eventService = eventService
		d.LastSeen = time.Now()
	})
	if !ok {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	if err := s.saveDevice(updatedDevice); err != nil {
		log.Printf("failed to persist refreshed device %s: %v", updatedDevice.Name, err)
		writeError(w, http.StatusInternalServerError, "Failed to save device state")
		return
	}

	s.updateEventWorker(deviceID)
	writeJSON(w, http.StatusOK, updatedDevice)
}

func (s *Server) persistRefreshFailure(deviceID string) {
	if snapshot, ok := s.mutateDevice(deviceID, func(d *Device) {
		d.Connected = false
		d.LastSeen = time.Now()
	}); ok {
		if err := s.saveDevice(snapshot); err != nil {
			log.Printf("failed to persist device %s after refresh failure: %v", deviceID, err)
		}
		s.updateEventWorker(deviceID)
	}
}

func (s *Server) mutateDevice(deviceID string, apply func(*Device)) (*Device, bool) {
	s.devicesMu.Lock()
	device, exists := s.devices[deviceID]
	if !exists {
		s.devicesMu.Unlock()
		return nil, false
	}
	if apply != nil {
		apply(device)
	}
	if device.EventIntervalSeconds < 1 {
		device.EventIntervalSeconds = 1
	}
	copy := *device
	s.devicesMu.Unlock()
	return &copy, true
}

// generateDeviceID generates a device ID from endpoint
// checkDeviceAccess checks if the request has access to a device
func (s *Server) checkDeviceAccess(r *http.Request, deviceID string) (*Device, bool) {
	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		return nil, false
	}

	user := s.getCurrentUser(r)
	if user != nil && s.userHasCameraAccess(user, deviceID) {
		return device, true
	}

	if device.IsPublic {
		return device, true
	}

	return nil, false
}

// checkPTZAccess checks if the request has PTZ access to a device
func (s *Server) checkPTZAccess(r *http.Request, deviceID string) bool {
	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		return false
	}

	user := s.getCurrentUser(r)
	if user != nil && s.userHasPTZAccess(user, deviceID) {
		return true
	}

	if device.IsPublic && device.AllowPublicPTZ {
		return true
	}

	return false
}

// handleUserSnapshot handles creating and listing snapshots
func (s *Server) handleUserSnapshot(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	deviceID := parts[0]

	// Check access
	user := s.getCurrentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if !s.userHasCameraAccess(user, deviceID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if r.Method == http.MethodGet {
		snapshots, err := s.listUserSnapshots(deviceID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, snapshots)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Image        string `json:"image"` // Base64 encoded image
			ProfileToken string `json:"profileToken"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON")
			return
		}

		var imageData []byte
		var err error

		if req.Image != "" {
			// Image provided by client (canvas capture)
			// Remove data:image/jpeg;base64, prefix if present
			b64 := req.Image
			if idx := strings.Index(b64, ","); idx != -1 {
				b64 = b64[idx+1:]
			}
			imageData, err = base64.StdEncoding.DecodeString(b64)
			if err != nil {
				writeError(w, http.StatusBadRequest, "Invalid base64 image")
				return
			}
		} else {
			// Capture from camera
			s.devicesMu.RLock()
			device, exists := s.devices[deviceID]
			s.devicesMu.RUnlock()
			if !exists {
				writeError(w, http.StatusNotFound, "Device not found")
				return
			}

			profileToken := req.ProfileToken
			if profileToken == "" && len(device.Profiles) > 0 {
				profileToken = device.Profiles[0].Token
			}

			imageData, _, err = s.fetchSnapshotData(device, profileToken)
			if err != nil {
				writeError(w, http.StatusBadGateway, "Failed to capture snapshot: "+err.Error())
				return
			}
			// Normalize to JPEG if needed
			imageData, err = normalizeToJPEG(imageData, "image/jpeg") // Assuming JPEG for now or detect
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to process image: "+err.Error())
				return
			}
		}

		if err := s.saveUserSnapshot(deviceID, user.Username, imageData); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save snapshot: "+err.Error())
			return
		}

		// Update system thumbnail as well, since we have a fresh image
		if err := s.saveThumbnailBytes(deviceID, imageData); err != nil {
			log.Printf("Failed to update system thumbnail from snapshot for %s: %v", deviceID, err)
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

// handleUserSnapshotDelete handles deleting snapshots
func (s *Server) handleUserSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 4 { // /api/devices/{id}/snapshots/{filename}
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	deviceID := parts[0]
	filename := parts[2]

	// Check access
	user := s.getCurrentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if !s.userHasCameraAccess(user, deviceID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	if err := s.deleteUserSnapshot(deviceID, filename); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "Snapshot not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleUserSnapshotFile serves snapshot files
func (s *Server) handleUserSnapshotFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 3 { // /api/devices/{id}/snapshots/{filename}
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}
	deviceID := parts[0]
	filename := parts[2]

	// Check access
	user := s.getCurrentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if !s.userHasCameraAccess(user, deviceID) {
		writeError(w, http.StatusForbidden, "Access denied")
		return
	}

	path := filepath.Join(snapshotDirPath(deviceID), filename)
	http.ServeFile(w, r, path)
}
