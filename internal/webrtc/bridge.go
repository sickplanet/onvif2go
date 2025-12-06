package webrtc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/format/rtph264"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

// Constants for RTP encoding
const (
	rtpPayloadType    = 96
	rtpPayloadMaxSize = 1200
)

// RTSPToWebRTCBridge bridges RTSP streams to WebRTC
type RTSPToWebRTCBridge struct {
	rtspURL    string
	username   string
	password   string
	peerConn   *webrtc.PeerConnection
	rtspClient *gortsplib.Client
	videoTrack *webrtc.TrackLocalStaticRTP
	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	running    bool
	lastError  error
}

// StreamManager manages multiple WebRTC streams
type StreamManager struct {
	bridges map[string]*RTSPToWebRTCBridge
	mu      sync.RWMutex
}

// NewStreamManager creates a new stream manager
func NewStreamManager() *StreamManager {
	return &StreamManager{
		bridges: make(map[string]*RTSPToWebRTCBridge),
	}
}

// GetOrCreateBridge gets or creates a bridge for a stream
func (sm *StreamManager) GetOrCreateBridge(streamID, rtspURL, username, password string) (*RTSPToWebRTCBridge, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if bridge, ok := sm.bridges[streamID]; ok {
		return bridge, nil
	}

	bridge, err := NewRTSPToWebRTCBridge(rtspURL, username, password)
	if err != nil {
		return nil, err
	}

	sm.bridges[streamID] = bridge
	return bridge, nil
}

// RemoveBridge removes and stops a bridge
func (sm *StreamManager) RemoveBridge(streamID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if bridge, ok := sm.bridges[streamID]; ok {
		bridge.Stop()
		delete(sm.bridges, streamID)
	}
}

// NewRTSPToWebRTCBridge creates a new RTSP to WebRTC bridge
func NewRTSPToWebRTCBridge(rtspURL, username, password string) (*RTSPToWebRTCBridge, error) {
	ctx, cancel := context.WithCancel(context.Background())

	bridge := &RTSPToWebRTCBridge{
		rtspURL:  rtspURL,
		username: username,
		password: password,
		ctx:      ctx,
		cancel:   cancel,
	}

	return bridge, nil
}

// CreateOffer creates a WebRTC offer
func (b *RTSPToWebRTCBridge) CreateOffer() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Create peer connection config with STUN servers
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	b.peerConn = peerConn

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"onvif-stream",
	)
	if err != nil {
		return "", fmt.Errorf("failed to create video track: %w", err)
	}

	b.videoTrack = videoTrack

	_, err = peerConn.AddTrack(videoTrack)
	if err != nil {
		return "", fmt.Errorf("failed to add track: %w", err)
	}

	// Handle ICE connection state changes
	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state changed: %s", state.String())
		if state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			b.Stop()
		}
	})

	// Create offer
	offer, err := peerConn.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	// Set local description
	err = peerConn.SetLocalDescription(offer)
	if err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConn)
	<-gatherComplete

	return peerConn.LocalDescription().SDP, nil
}

// SetAnswer sets the WebRTC answer
func (b *RTSPToWebRTCBridge) SetAnswer(answerSDP string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.peerConn == nil {
		return errors.New("peer connection not initialized")
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}

	err := b.peerConn.SetRemoteDescription(answer)
	if err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	return nil
}

// HandleAnswer processes a WebRTC answer and starts streaming
func (b *RTSPToWebRTCBridge) HandleAnswer(answerSDP string) error {
	if err := b.SetAnswer(answerSDP); err != nil {
		return err
	}

	// Start RTSP to WebRTC streaming
	go b.startStreaming()

	return nil
}

// startStreaming starts streaming from RTSP to WebRTC
func (b *RTSPToWebRTCBridge) startStreaming() {
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return
	}
	b.running = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	// Parse RTSP URL
	u, err := url.Parse(b.rtspURL)
	if err != nil {
		b.lastError = fmt.Errorf("failed to parse RTSP URL: %w", err)
		log.Printf("RTSP URL parse error: %v", b.lastError)
		return
	}

	// Add credentials if provided
	if b.username != "" && b.password != "" {
		u.User = url.UserPassword(b.username, b.password)
	}

	// Create RTSP client
	b.rtspClient = &gortsplib.Client{}

	// Connect to RTSP server
	err = b.rtspClient.Start(u.Scheme, u.Host)
	if err != nil {
		b.lastError = fmt.Errorf("failed to connect to RTSP server: %w", err)
		log.Printf("RTSP connection error: %v", b.lastError)
		return
	}
	defer b.rtspClient.Close()

	// Get stream description (preserve credentials and query params)
	baseURL := base.URL(*u)
	desc, _, err := b.rtspClient.Describe(&baseURL)
	if err != nil {
		b.lastError = fmt.Errorf("failed to describe stream: %w", err)
		log.Printf("RTSP describe error: %v", b.lastError)
		return
	}

	// Find H264 track
	var h264Format *format.H264
	var h264Media *description.Media
	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			if f, ok := forma.(*format.H264); ok {
				h264Format = f
				h264Media = media
				break
			}
		}
	}

	if h264Format == nil {
		b.lastError = errors.New("H264 track not found")
		log.Printf("No H264 track found in RTSP stream")
		return
	}

	// Create H264 RTP encoder for WebRTC
	encoder := &rtph264.Encoder{
		PayloadType:    rtpPayloadType,
		PayloadMaxSize: rtpPayloadMaxSize,
	}
	err = encoder.Init()
	if err != nil {
		b.lastError = fmt.Errorf("failed to initialize H264 encoder: %w", err)
		log.Printf("H264 encoder init error: %v", b.lastError)
		return
	}
	_ = encoder // Encoder is initialized for potential future use

	// Setup media reading, ensuring authentication info is retained
	setupURL := desc.BaseURL
	if setupURL == nil {
		setupURL = &baseURL
	}
	if setupURL.User == nil && u.User != nil {
		clone := setupURL.Clone()
		clone.User = u.User
		setupURL = clone
	}

	_, err = b.rtspClient.Setup(setupURL, h264Media, 0, 0)
	if err != nil {
		b.lastError = fmt.Errorf("failed to setup RTSP media: %w", err)
		log.Printf("RTSP setup error: %v", b.lastError)
		return
	}

	// Handle RTP packets
	b.rtspClient.OnPacketRTP(h264Media, h264Format, func(pkt *rtp.Packet) {
		if b.videoTrack != nil {
			if err := b.videoTrack.WriteRTP(pkt); err != nil {
				log.Printf("Failed to write RTP packet: %v", err)
			}
		}
	})

	// Start playing
	_, err = b.rtspClient.Play(nil)
	if err != nil {
		b.lastError = fmt.Errorf("failed to play RTSP stream: %w", err)
		log.Printf("RTSP play error: %v", b.lastError)
		return
	}

	log.Printf("Started streaming RTSP to WebRTC: %s", b.rtspURL)

	// Wait for context cancellation
	<-b.ctx.Done()
	log.Printf("Streaming stopped for: %s", b.rtspURL)
}

// Stop stops the bridge
func (b *RTSPToWebRTCBridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancel != nil {
		b.cancel()
	}

	if b.rtspClient != nil {
		b.rtspClient.Close()
		b.rtspClient = nil
	}

	if b.peerConn != nil {
		b.peerConn.Close()
		b.peerConn = nil
	}
}

// GetLastError returns the last error
func (b *RTSPToWebRTCBridge) GetLastError() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastError
}

// IsRunning checks if the bridge is running
func (b *RTSPToWebRTCBridge) IsRunning() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

// CreateAnswerHandler creates a peer connection that handles an offer and creates an answer
func CreateAnswerHandler(offerSDP, rtspURL, username, password string) (*RTSPToWebRTCBridge, string, error) {
	bridge, err := NewRTSPToWebRTCBridge(rtspURL, username, password)
	if err != nil {
		return nil, "", err
	}

	// Create peer connection config
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create peer connection: %w", err)
	}

	bridge.peerConn = peerConn

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"onvif-stream",
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create video track: %w", err)
	}

	bridge.videoTrack = videoTrack

	_, err = peerConn.AddTrack(videoTrack)
	if err != nil {
		return nil, "", fmt.Errorf("failed to add track: %w", err)
	}

	// Handle ICE connection state changes
	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state changed: %s", state.String())
		if state == webrtc.ICEConnectionStateDisconnected ||
			state == webrtc.ICEConnectionStateFailed ||
			state == webrtc.ICEConnectionStateClosed {
			bridge.Stop()
		}
	})

	// Set remote description (offer)
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}

	err = peerConn.SetRemoteDescription(offer)
	if err != nil {
		return nil, "", fmt.Errorf("failed to set remote description: %w", err)
	}

	// Create answer
	answer, err := peerConn.CreateAnswer(nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create answer: %w", err)
	}

	// Set local description
	err = peerConn.SetLocalDescription(answer)
	if err != nil {
		return nil, "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConn)
	select {
	case <-gatherComplete:
	case <-time.After(10 * time.Second):
		return nil, "", errors.New("ICE gathering timeout")
	}

	// Start streaming
	go bridge.startStreaming()

	return bridge, peerConn.LocalDescription().SDP, nil
}
