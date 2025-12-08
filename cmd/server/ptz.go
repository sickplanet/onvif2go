package main

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/sickplanet/onvif2go/internal/onvif"
)

// PTZMoveRequest represents a PTZ move request
type PTZMoveRequest struct {
	ProfileToken string  `json:"profileToken"`
	Pan          float64 `json:"pan"`
	Tilt         float64 `json:"tilt"`
	Zoom         float64 `json:"zoom"`
}

const ptzPositionTolerance = 0.01

func hasRequestedMovement(req PTZMoveRequest) bool {
	return math.Abs(req.Pan) > ptzPositionTolerance ||
		math.Abs(req.Tilt) > ptzPositionTolerance ||
		math.Abs(req.Zoom) > ptzPositionTolerance
}

func ptzPositionsEqual(a, b *onvif.PTZPosition) bool {
	if a == nil || b == nil {
		return false
	}

	return math.Abs(a.Pan-b.Pan) <= ptzPositionTolerance &&
		math.Abs(a.Tilt-b.Tilt) <= ptzPositionTolerance &&
		math.Abs(a.Zoom-b.Zoom) <= ptzPositionTolerance
}

func isPTZLimitError(err error, before, after *onvif.PTZPosition, movementRequested bool) bool {
	if movementRequested && ptzPositionsEqual(before, after) && before != nil && after != nil {
		return true
	}

	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	indicators := []string{
		"boundsexceeded",
		"bounds exceeded",
		"ptz bounds",
		"limit reached",
		"out of range",
	}

	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}

	return false
}

func isPresetMissingError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	indicators := []string{
		"nosuchpreset",
		"no such preset",
		"preset does not exist",
		"preset token",
		"preset not exist",
		"invalid preset",
		"token does not exist",
		"notoken",
		"invalidargval",
	}

	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}

	return false
}

// handlePTZMove handles PTZ move requests
func (s *Server) handlePTZMove(w http.ResponseWriter, r *http.Request) {
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

	var req PTZMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	profileToken := req.ProfileToken
	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	movementRequested := hasRequestedMovement(req)

	beforeStatus, err := client.GetPTZStatus(profileToken)
	if err != nil {
		log.Printf("Warning: unable to read PTZ status before move: %v", err)
	}

	moveErr := client.PTZMove(profileToken, struct{ X, Y float64 }{req.Pan, req.Tilt}, req.Zoom)

	if moveErr == nil {
		time.Sleep(300 * time.Millisecond)
	}

	afterStatus, err := client.GetPTZStatus(profileToken)
	if err != nil {
		log.Printf("Warning: unable to read PTZ status after move: %v", err)
	}

	if isPTZLimitError(moveErr, beforeStatus, afterStatus, movementRequested) {
		writeError(w, http.StatusConflict, "Camera has reached its movement limit")
		return
	}

	if moveErr != nil {
		writeError(w, http.StatusInternalServerError, moveErr.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePTZStop handles PTZ stop requests
func (s *Server) handlePTZStop(w http.ResponseWriter, r *http.Request) {
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
	profileToken := r.URL.Query().Get("profileToken")

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	err := client.PTZStop(profileToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetPresets returns PTZ presets
func (s *Server) handleGetPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusBadRequest, "Invalid path")
		return
	}

	deviceID := parts[0]
	profileToken := r.URL.Query().Get("profileToken")

	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	s.devicesMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "Device not found")
		return
	}

	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	presets, err := client.GetPresets(profileToken)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, presets)
}

// GotoPresetRequest represents a goto preset request
type GotoPresetRequest struct {
	ProfileToken string `json:"profileToken"`
	PresetToken  string `json:"presetToken"`
}

// handleGotoPreset handles going to a PTZ preset
func (s *Server) handleGotoPreset(w http.ResponseWriter, r *http.Request) {
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

	var req GotoPresetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	profileToken := req.ProfileToken
	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	client := onvif.NewClient(device.Endpoint, device.Username, device.Password)

	err := client.PTZGotoPreset(profileToken, req.PresetToken)
	if err != nil {
		if isPresetMissingError(err) {
			writeError(w, http.StatusConflict, "Preset not configured on camera")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
