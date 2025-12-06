package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/sickplanet/onvif2go/internal/onvif"
	"github.com/sickplanet/onvif2go/internal/webrtc"

	bolt "go.etcd.io/bbolt"
)

// Server represents the web server
type Server struct {
	devices       map[string]*Device
	devicesMu     sync.RWMutex
	streamManager *webrtc.StreamManager
	userStore     *UserStore
	sessionStore  *SessionStore
	eventManager  *EventManager
	deviceStore   *DeviceStore
	rtspService   *RTSPService
	db            *bolt.DB
}

func (s *Server) getDeviceSnapshot(deviceID string) (*Device, bool) {
	s.devicesMu.RLock()
	defer s.devicesMu.RUnlock()
	device, exists := s.devices[deviceID]
	if !exists {
		return nil, false
	}
	copy := *device
	if copy.EventIntervalSeconds < 1 {
		copy.EventIntervalSeconds = 1
	}
	return &copy, true
}

func (s *Server) updateEventWorker(deviceID string) {
	if s.eventManager == nil {
		return
	}
	s.devicesMu.RLock()
	device, exists := s.devices[deviceID]
	if !exists {
		s.devicesMu.RUnlock()
		s.eventManager.Disable(deviceID)
		return
	}
	supported := device.SupportsEvents && device.eventService != ""
	enabled := device.EventsEnabled
	s.devicesMu.RUnlock()
	if supported && enabled {
		s.eventManager.Enable(deviceID)
	} else {
		s.eventManager.Disable(deviceID)
	}
}

// NewServer creates a new server
func NewServer() (*Server, error) {
	db, err := openDatabase(defaultDBPath)
	if err != nil {
		return nil, err
	}
	userStore, err := NewUserStore(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	server := &Server{
		db:            db,
		devices:       make(map[string]*Device),
		streamManager: webrtc.NewStreamManager(),
		userStore:     userStore,
		sessionStore:  NewSessionStore(db),
		deviceStore:   NewDeviceStore(db),
	}
	server.eventManager = NewEventManager(server)
	if err := server.loadDevicesFromStore(); err != nil {
		db.Close()
		return nil, err
	}
	server.ensureUUIDDeviceIDs()
	server.restoreEventWorkers()
	return server, nil
}

func (s *Server) ensureUUIDDeviceIDs() {
	s.devicesMu.Lock()
	defer s.devicesMu.Unlock()

	migratedCount := 0
	// Create a map of changes to avoid modifying map while iterating
	changes := make(map[string]*Device)
	deletions := make([]string, 0)

	for oldID, device := range s.devices {
		if isLegacyID(oldID) {
			// Generate new ID
			newID := generateUUID()

			// Update device object
			device.ID = newID

			// Save with new ID
			if err := s.deviceStore.Save(device); err != nil {
				log.Printf("Failed to save migrated device %s: %v", oldID, err)
				continue
			}

			// Delete old ID from store
			if err := s.deviceStore.Delete(oldID); err != nil {
				log.Printf("Failed to delete old device %s: %v", oldID, err)
			}

			// Update users
			if err := s.userStore.ReplaceDeviceRefs(oldID, newID); err != nil {
				log.Printf("Failed to update user refs for %s: %v", oldID, err)
			}

			// Rename thumbnail
			renameThumbnailFile(oldID, newID)

			changes[newID] = device
			deletions = append(deletions, oldID)

			migratedCount++
			log.Printf("Migrated camera ID from %s to %s", oldID, newID)
		}
	}

	for _, oldID := range deletions {
		delete(s.devices, oldID)
	}
	for newID, device := range changes {
		s.devices[newID] = device
	}

	if migratedCount > 0 {
		log.Printf("Migrated %d devices to UUIDs", migratedCount)
	}
}

func isLegacyID(id string) bool {
	// Legacy IDs contain IP addresses or underscores from sanitization
	// UUIDs are hex strings with dashes (36 chars)
	if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' {
		return false
	}
	return true
}

func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Close releases database resources.
func (s *Server) Close() error {
	if s.rtspService != nil {
		s.rtspService.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Server) loadDevicesFromStore() error {
	if s.deviceStore == nil {
		return nil
	}
	devices, err := s.deviceStore.LoadAll()
	if err != nil {
		return err
	}
	s.devicesMu.Lock()
	s.devices = devices
	s.devicesMu.Unlock()
	return nil
}

func (s *Server) restoreEventWorkers() {
	if s.eventManager == nil {
		return
	}
	s.devicesMu.RLock()
	defer s.devicesMu.RUnlock()
	for id, device := range s.devices {
		if device.SupportsEvents && device.EventsEnabled && device.eventService != "" {
			s.eventManager.Enable(id)
		}
	}
}

func (s *Server) saveDevice(device *Device) error {
	if s.deviceStore == nil || device == nil {
		return nil
	}
	return s.deviceStore.Save(device)
}

func (s *Server) deleteDeviceRecord(id string) error {
	if s.deviceStore == nil {
		return nil
	}
	return s.deviceStore.Delete(id)
}

// writeJSON writes JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes an error response
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, APIError{Error: http.StatusText(status), Message: message})
}

// getSessionToken extracts the session token from the request
func getSessionToken(r *http.Request) string {
	cookie, err := r.Cookie("session")
	if err != nil {
		return ""
	}
	return cookie.Value
}

// getCurrentUser returns the current authenticated user from request
func (s *Server) getCurrentUser(r *http.Request) *User {
	token := getSessionToken(r)
	if token == "" {
		return nil
	}
	session := s.sessionStore.Get(token)
	if session == nil {
		return nil
	}
	return s.userStore.Get(session.Username)
}

// requireAuth is middleware that requires authentication
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.getCurrentUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		next(w, r)
	}
}

// requireAdmin is middleware that requires admin authentication
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := s.getCurrentUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "Admin access required")
			return
		}
		next(w, r)
	}
}

// userHasCameraAccess checks if user has access to a device
func (s *Server) userHasCameraAccess(user *User, deviceID string) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	for _, cam := range user.Cameras {
		if cam == deviceID {
			return true
		}
	}
	return false
}

// userHasPTZAccess checks if user has PTZ control access
func (s *Server) userHasPTZAccess(user *User, deviceID string) bool {
	if user == nil {
		return false
	}
	if user.IsAdmin {
		return true
	}
	for _, ptz := range user.PTZAllowed {
		if ptz == deviceID {
			return true
		}
	}
	return false
}

// Helper to get ONVIF client
func (s *Server) getOnvifClient(device *Device) *onvif.Client {
	return onvif.NewClient(device.Endpoint, device.Username, device.Password)
}
