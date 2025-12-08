package main

import (
	"encoding/base64"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/pion/rtp"
)

// RTSPService handles RTSP proxying
type RTSPService struct {
	server        *gortsplib.Server
	app           *Server
	streams       map[string]*RTSPStream
	mu            sync.Mutex
	internalToken string
}

// RTSPStream represents an active proxy stream
type RTSPStream struct {
	client       *gortsplib.Client
	serverStream *gortsplib.ServerStream
	lastAccess   time.Time
}

// NewRTSPService creates a new RTSP service
func NewRTSPService(app *Server, address string) *RTSPService {
	s := &RTSPService{
		app:           app,
		streams:       make(map[string]*RTSPStream),
		internalToken: generateUUID(),
	}

	s.server = &gortsplib.Server{
		Handler:     s,
		RTSPAddress: address,
		// Disable UDP to ensure WAN compatibility (TCP only)
		UDPRTPAddress:  "",
		UDPRTCPAddress: "",
	}

	// Start cleanup routine
	go s.cleanupLoop()

	return s
}

// Start starts the RTSP server
func (s *RTSPService) Start() error {
	//log.Printf("Starting RTSP server on %s", s.server.RTSPAddress)
	return s.server.Start()
}

// Close stops the RTSP server
func (s *RTSPService) Close() {
	s.server.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stream := range s.streams {
		stream.client.Close()
		stream.serverStream.Close()
	}
}

// cleanupLoop removes unused streams
func (s *RTSPService) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, stream := range s.streams {
			// Close if idle for 1 hour
			// We use a long timeout because we can't easily track active readers
			// without OnPacketRTCP (which had type issues) or OnSessionClose.
			if now.Sub(stream.lastAccess) > 1*time.Hour {
				log.Printf("Closing idle RTSP stream for %s", id)
				stream.client.Close()
				stream.serverStream.Close()
				delete(s.streams, id)
			}
		}
		s.mu.Unlock()
	}
}

// OnDescribe handles RTSP DESCRIBE requests
func (s *RTSPService) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	path := ctx.Path
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return &base.Response{StatusCode: base.StatusBadRequest}, nil, nil
	}
	deviceID := parts[0]
	profileToken := ""
	if len(parts) > 1 {
		profileToken = parts[1]
	}

	// Check access
	s.app.devicesMu.RLock()
	device, exists := s.app.devices[deviceID]
	s.app.devicesMu.RUnlock()

	values, _ := url.ParseQuery(ctx.Query)
	token := values.Get("token")
	isInternal := token == s.internalToken

	if !exists {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	if !device.IsPublic && !isInternal {
		// Check authentication
		authHeader := ctx.Request.Header["Authorization"]
		if len(authHeader) == 0 {
			return &base.Response{
				StatusCode: base.StatusUnauthorized,
				Header: base.Header{
					"WWW-Authenticate": base.HeaderValue{`Basic realm="onvif2go"`},
				},
			}, nil, nil
		}

		// Parse Basic Auth
		authParts := strings.SplitN(authHeader[0], " ", 2)
		if len(authParts) != 2 || authParts[0] != "Basic" {
			return &base.Response{StatusCode: base.StatusUnauthorized}, nil, nil
		}

		payload, err := base64.StdEncoding.DecodeString(authParts[1])
		if err != nil {
			return &base.Response{StatusCode: base.StatusUnauthorized}, nil, nil
		}

		pair := strings.SplitN(string(payload), ":", 2)
		if len(pair) != 2 {
			return &base.Response{StatusCode: base.StatusUnauthorized}, nil, nil
		}

		username, password := pair[0], pair[1]
		user := s.app.userStore.Get(username)
		if user == nil || !checkPassword(password, user.PasswordHash) {
			return &base.Response{StatusCode: base.StatusUnauthorized}, nil, nil
		}

		// Check permissions
		allowed := user.IsAdmin
		if !allowed {
			for _, allowedID := range user.RTSPAllowed {
				if allowedID == deviceID {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			return &base.Response{StatusCode: base.StatusForbidden}, nil, nil
		}
	}

	// Default profile if not specified
	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	streamKey := deviceID + ":" + profileToken

	s.mu.Lock()
	if stream, ok := s.streams[streamKey]; ok {
		stream.lastAccess = time.Now()
		s.mu.Unlock()
		return &base.Response{StatusCode: base.StatusOK}, stream.serverStream, nil
	}
	s.mu.Unlock()

	// Create new stream
	log.Printf("Starting new RTSP proxy stream for %s (profile: %s)", deviceID, profileToken)

	client := &gortsplib.Client{
		// Increase timeout for slow cameras
		ReadTimeout: 10 * time.Second,
	}

	// Get stream URI
	onvifClient := s.app.getOnvifClient(device)
	streamURI, err := onvifClient.GetStreamURI(profileToken)
	if err != nil {
		log.Printf("Failed to get stream URI: %v", err)
		return &base.Response{StatusCode: base.StatusBadGateway}, nil, nil
	}

	u, err := base.ParseURL(streamURI)
	if err != nil {
		return &base.Response{StatusCode: base.StatusInternalServerError}, nil, nil
	}

	// Add credentials
	if device.Username != "" {
		u.User = url.UserPassword(device.Username, device.Password)
	}

	// Connect to camera
	err = client.Start(u.Scheme, u.Host)
	if err != nil {
		log.Printf("Failed to connect to camera: %v", err)
		return &base.Response{StatusCode: base.StatusBadGateway}, nil, nil
	}

	// Describe
	desc, _, err := client.Describe(u)
	if err != nil {
		client.Close()
		log.Printf("Failed to describe stream: %v", err)
		return &base.Response{StatusCode: base.StatusBadGateway}, nil, nil
	}

	// Setup
	err = client.SetupAll(desc.BaseURL, desc.Medias)
	if err != nil {
		client.Close()
		log.Printf("Failed to setup stream: %v", err)
		return &base.Response{StatusCode: base.StatusBadGateway}, nil, nil
	}

	// Create server stream
	serverStream := gortsplib.NewServerStream(s.server, desc)

	// Forward packets
	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			cMedia := media
			cForma := forma
			client.OnPacketRTP(cMedia, cForma, func(pkt *rtp.Packet) {
				serverStream.WritePacketRTP(cMedia, pkt)
			})
		}
	}

	// Play
	_, err = client.Play(nil)
	if err != nil {
		client.Close()
		serverStream.Close()
		log.Printf("Failed to play stream: %v", err)
		return &base.Response{StatusCode: base.StatusBadGateway}, nil, nil
	}

	stream := &RTSPStream{
		client:       client,
		serverStream: serverStream,
		lastAccess:   time.Now(),
	}

	s.mu.Lock()
	// Double check
	if existing, ok := s.streams[streamKey]; ok {
		s.mu.Unlock()
		client.Close()
		serverStream.Close()
		existing.lastAccess = time.Now()
		return &base.Response{StatusCode: base.StatusOK}, existing.serverStream, nil
	}
	s.streams[streamKey] = stream
	s.mu.Unlock()

	// Monitor client connection
	go func() {
		client.Wait()
		s.mu.Lock()
		if s.streams[streamKey] == stream {
			delete(s.streams, streamKey)
			serverStream.Close()
		}
		s.mu.Unlock()
	}()

	return &base.Response{StatusCode: base.StatusOK}, serverStream, nil
}

// OnPlay handles RTSP PLAY requests
func (s *RTSPService) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	// Update last access
	path := ctx.Path
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		deviceID := parts[0]
		profileToken := ""
		if len(parts) > 1 {
			profileToken = parts[1]
		}

		s.app.devicesMu.RLock()
		device, exists := s.app.devices[deviceID]
		s.app.devicesMu.RUnlock()

		if exists {
			if profileToken == "" && len(device.Profiles) > 0 {
				profileToken = device.Profiles[0].Token
			}
			streamKey := deviceID + ":" + profileToken

			s.mu.Lock()
			if stream, ok := s.streams[streamKey]; ok {
				stream.lastAccess = time.Now()
			}
			s.mu.Unlock()
		}
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnSetup handles RTSP SETUP requests
func (s *RTSPService) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	path := ctx.Path
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return &base.Response{StatusCode: base.StatusBadRequest}, nil, nil
	}
	deviceID := parts[0]
	profileToken := ""
	if len(parts) > 1 {
		profileToken = parts[1]
	}

	s.app.devicesMu.RLock()
	device, exists := s.app.devices[deviceID]
	s.app.devicesMu.RUnlock()

	if !exists {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	if profileToken == "" && len(device.Profiles) > 0 {
		profileToken = device.Profiles[0].Token
	}

	streamKey := deviceID + ":" + profileToken

	s.mu.Lock()
	stream, ok := s.streams[streamKey]
	s.mu.Unlock()

	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, stream.serverStream, nil
}

func (s *RTSPService) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusMethodNotAllowed}, nil
}

func (s *RTSPService) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusMethodNotAllowed}, nil
}

func (s *RTSPService) OnPause(ctx *gortsplib.ServerHandlerOnPauseCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusMethodNotAllowed}, nil
}

func (s *RTSPService) OnGetParameter(ctx *gortsplib.ServerHandlerOnGetParameterCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (s *RTSPService) OnSetParameter(ctx *gortsplib.ServerHandlerOnSetParameterCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusMethodNotAllowed}, nil
}
