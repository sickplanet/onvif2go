package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/sickplanet/onvif2go/internal/onvif"
)

//go:embed all:web
var webFS embed.FS

// Version is the current application version
var Version = "1.0.0"

// noCache middleware sets headers to disable caching
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		h.ServeHTTP(w, r)
	})
}

// setupRoutes sets up all API routes
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Auth routes (no authentication required)
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": Version})
	})
	mux.HandleFunc("/api/auth/login", s.handleLogin)
	mux.HandleFunc("/api/auth/logout", s.handleLogout)
	mux.HandleFunc("/api/auth/me", s.handleMe)
	mux.HandleFunc("/api/auth/first-user", s.handleFirstUser)
	mux.HandleFunc("/api/auth/needs-setup", s.handleNeedsSetup)

	// User management routes (admin only)
	mux.HandleFunc("/api/users", s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListUsers(w, r)
		case http.MethodPost:
			s.handleCreateUser(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))
	mux.HandleFunc("/api/users/", s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		username := strings.TrimPrefix(r.URL.Path, "/api/users/")
		switch r.Method {
		case http.MethodPut:
			s.handleUpdateUser(w, r, username)
		case http.MethodDelete:
			s.handleDeleteUser(w, r, username)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// Public camera routes (no authentication required)
	mux.HandleFunc("/public/cameras", s.handlePublicCameras)
	mux.HandleFunc("/public/cameras/", s.handlePublicCamera)
	mux.HandleFunc("/public/thumbnails/", s.handlePublicThumbnail)

	// Discover routes (admin only)
	mux.HandleFunc("/api/discover", s.requireAdmin(s.handleDiscover))
	mux.HandleFunc("/api/credentials/test", s.requireAdmin(s.handleTestCredentials))

	// Device routes
	mux.HandleFunc("/api/devices", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.handleListDevices(w, r)
		case http.MethodPost:
			// Only admins can add devices
			user := s.getCurrentUser(r)
			if user == nil || !user.IsAdmin {
				writeError(w, http.StatusForbidden, "Admin access required")
				return
			}
			s.handleAddDevice(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}))

	// Device-specific routes
	mux.HandleFunc("/api/devices/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/devices/")
		parts := strings.Split(path, "/")

		if len(parts) == 0 || parts[0] == "" {
			writeError(w, http.StatusBadRequest, "Device ID required")
			return
		}

		deviceID := parts[0]

		if len(parts) == 1 {
			// /api/devices/{id}
			switch r.Method {
			case http.MethodGet:
				// Check access
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
				writeJSON(w, http.StatusOK, device)
			case http.MethodDelete:
				// Only admins can delete
				user := s.getCurrentUser(r)
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Authentication required")
					return
				}
				if !user.IsAdmin {
					writeError(w, http.StatusForbidden, "Admin access required")
					return
				}
				s.handleDeleteDevice(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
			}
			return
		}

		if len(parts) >= 2 {
			action := parts[1]
			switch action {
			case "config":
				// Config requires authentication (admin for public settings)
				user := s.getCurrentUser(r)
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Authentication required")
					return
				}
				if !s.userHasCameraAccess(user, deviceID) {
					writeError(w, http.StatusForbidden, "Access denied")
					return
				}
				s.handleDeviceConfig(w, r)
			case "stream":
				if len(parts) >= 3 {
					_, ok := s.checkDeviceAccess(r, deviceID)
					if !ok {
						user := s.getCurrentUser(r)
						if user == nil {
							writeError(w, http.StatusUnauthorized, "Authentication required")
						} else {
							writeError(w, http.StatusForbidden, "Access denied")
						}
						return
					}
					s.handleGetStreamURI(w, r)
				} else {
					writeError(w, http.StatusBadRequest, "Profile token required")
				}
			case "webrtc":
				_, ok := s.checkDeviceAccess(r, deviceID)
				if !ok {
					user := s.getCurrentUser(r)
					if user == nil {
						writeError(w, http.StatusUnauthorized, "Authentication required")
					} else {
						writeError(w, http.StatusForbidden, "Access denied")
					}
					return
				}
				s.handleWebRTCStream(w, r)
			case "snapshots":
				// Check access is handled inside the handler
				if len(parts) >= 3 {
					// /api/devices/{id}/snapshots/{filename}
					if r.Method == http.MethodDelete {
						s.handleUserSnapshotDelete(w, r)
					} else {
						s.handleUserSnapshotFile(w, r)
					}
				} else {
					// /api/devices/{id}/snapshots
					s.handleUserSnapshot(w, r)
				}
			case "thumbnail":
				s.handleUploadThumbnail(w, r)
			case "ptz":
				if len(parts) >= 3 {
					// Check PTZ access
					if !s.checkPTZAccess(r, deviceID) {
						user := s.getCurrentUser(r)
						if user == nil {
							writeError(w, http.StatusUnauthorized, "Authentication required")
						} else {
							writeError(w, http.StatusForbidden, "PTZ access denied")
						}
						return
					}
					switch parts[2] {
					case "move":
						s.handlePTZMove(w, r)
					case "stop":
						s.handlePTZStop(w, r)
					case "presets":
						s.handleGetPresets(w, r)
					case "goto":
						s.handleGotoPreset(w, r)
					default:
						writeError(w, http.StatusNotFound, "Unknown PTZ action")
					}
				} else {
					writeError(w, http.StatusBadRequest, "PTZ action required")
				}
			case "snapshot":
				if len(parts) >= 3 {
					_, ok := s.checkDeviceAccess(r, deviceID)
					if !ok {
						user := s.getCurrentUser(r)
						if user == nil {
							writeError(w, http.StatusUnauthorized, "Authentication required")
						} else {
							writeError(w, http.StatusForbidden, "Access denied")
						}
						return
					}
					s.handleProxySnapshot(w, r)
				} else {
					writeError(w, http.StatusBadRequest, "Profile token required")
				}
			case "refresh":
				user := s.getCurrentUser(r)
				if user == nil {
					writeError(w, http.StatusUnauthorized, "Authentication required")
					return
				}
				if !s.userHasCameraAccess(user, deviceID) {
					writeError(w, http.StatusForbidden, "Access denied")
					return
				}
				s.handleRefreshDevice(w, r)
			case "events":
				if len(parts) >= 3 && parts[2] == "stream" {
					s.handleDeviceEventsStream(w, r)
				} else {
					writeError(w, http.StatusBadRequest, "Unknown events action")
				}
			default:
				writeError(w, http.StatusNotFound, "Unknown action")
			}
		}
	})

	// Static files
	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(webContent))
	mux.Handle("/", noCache(fileServer))
}

type Config struct {
	Port     int    `json:"port"`
	RTSPPort int    `json:"rtsp-port"`
	Debug    bool   `json:"debug"`
	UseSSL   bool   `json:"useSSL"`
	SSLCert  string `json:"sslCert"`
	SSLKey   string `json:"sslKey"`
	Log2File string `json:"log2File"`
}

func loadConfig() (*Config, error) {
	configPath := "onvif2go.config"
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		absPath = configPath
	}

	// Check if config exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config
		config := &Config{
			Port:     8080,
			RTSPPort: 8554,
			Debug:    false,
			UseSSL:   false,
			SSLCert:  "",
			SSLKey:   "",
			Log2File: "",
		}

		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return nil, err
		}

		comment := `// ONVIF2GO Configuration
// This file contains the server configuration.
// Lines starting with // are comments and will be ignored.
// Command line arguments override these values.
`
		content := append([]byte(comment), data...)

		if err := os.WriteFile(configPath, content, 0644); err != nil {
			return nil, err
		}

		log.Printf("Default config created at %s", absPath)

		return config, nil
	}

	// Read existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	// Find start of JSON object to ignore comments
	idx := bytes.IndexByte(data, '{')
	if idx == -1 {
		return nil, fmt.Errorf("invalid config file: no JSON object found")
	}

	var config Config
	if err := json.Unmarshal(data[idx:], &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func main() {
	// Check if arguments are provided
	useConfig := len(os.Args) == 1

	port := flag.Int("port", 8080, "Server port")
	rtspPort := flag.Int("rtsp-port", 8554, "RTSP Server port")
	debugFlag := flag.Bool("debug", false, "Enable verbose debug logging")
	flag.Parse()

	var config *Config
	if useConfig {
		var err error
		config, err = loadConfig()
		if err != nil {
			log.Printf("Failed to load config: %v, using defaults", err)
			config = &Config{
				Port:     *port,
				RTSPPort: *rtspPort,
				Debug:    *debugFlag,
			}
		}
	} else {
		config = &Config{
			Port:     *port,
			RTSPPort: *rtspPort,
			Debug:    *debugFlag,
		}
	}

	debugEnabled = config.Debug || envBool("DEBUG")
	onvif.Debug = debugEnabled
	if debugEnabled {
		log.Printf("Debug logging enabled")
	}

	server, err := NewServer()
	if err != nil {
		log.Fatalf("failed to initialize server: %v", err)
	}
	defer server.Close()

	// Start RTSP Server
	server.rtspService = NewRTSPService(server, fmt.Sprintf(":%d", config.RTSPPort))
	if err := server.rtspService.Start(); err != nil {
		log.Printf("Failed to start RTSP server: %v", err)
	}

	mux := http.NewServeMux()
	server.setupRoutes(mux)

	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("RTSP Server listening on rtsp://localhost:%d", config.RTSPPort)

	if config.UseSSL {
		if config.SSLCert == "" || config.SSLKey == "" {
			//log.Fatal("SSL enabled but sslCert or sslKey not specified in config")
			// Fallback to non-SSL
			log.Printf("SSL enabled but sslCert or sslKey not specified in config, falling back to non-SSL")
			log.Printf("Starting ONVIF2GO v%s on http://localhost%s", Version, addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Fatal(err)
			}

		} else {
			log.Printf("Starting ONVIF2GO v%s on https://localhost%s", Version, addr)
			if err := http.ListenAndServeTLS(addr, config.SSLCert, config.SSLKey, mux); err != nil {
				log.Fatal(err)
			}
		}
	} else {
		log.Printf("Starting ONVIF2GO v%s on http://localhost%s", Version, addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Fatal(err)
		}
	}
}
