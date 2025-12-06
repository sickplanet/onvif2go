# ONVIF Device Manager (Go + Web UI)

A modern ONVIF Device Manager rewritten in Go with a web-based user interface. This is a reimplementation of the original [ONVIF Device Manager](https://github.com/dxball/ONVIF-Device-Manager) with enhanced features including WebRTC streaming support.

## Features

- 🔍 **Device Discovery** - Automatically discover ONVIF-compatible cameras on your network using WS-Discovery
- 📹 **Live Streaming** - View live video streams via WebRTC (RTSP to WebRTC conversion)
- 🎮 **PTZ Control** - Pan, Tilt, and Zoom controls for PTZ-enabled cameras
- 📷 **Snapshot Capture** - Take snapshots from camera streams
- 🔐 **Authentication** - Support for ONVIF digest authentication
- 💻 **Web Interface** - Modern, responsive web UI that works in any browser

## Screenshots

The web interface provides a clean, intuitive layout with:
- Device sidebar for managing multiple cameras
- Live streaming panel with profile selection
- PTZ controls with preset support
- Device information display

## Requirements

- Go 1.21 or later
- Network access to ONVIF-compatible cameras

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/sickplanet/ONVIF-Device-Manager-Rewritten-In-Go-with-web-ui.git
cd ONVIF-Device-Manager-Rewritten-In-Go-with-web-ui

# Build the application
go build -o onvif-manager ./cmd/server

# Run the server
./onvif-manager -port 8080
```

### Using Go Install

```bash
go install github.com/sickplanet/onvif-device-manager/cmd/server@latest
```

## Usage

### Starting the Server

```bash
./onvif-manager -port 8080
```

Then open your browser and navigate to `http://localhost:8080`

### Command Line Options

| Option | Default | Description |
|--------|---------|-------------|
| `-port` | 8080 | HTTP server port |

## API Endpoints

### Device Discovery
- `POST /api/discover` - Discover ONVIF devices on the network

### Device Management
- `GET /api/devices` - List all devices
- `POST /api/devices` - Add a new device
- `GET /api/devices/{id}` - Get device details
- `DELETE /api/devices/{id}` - Remove a device
- `POST /api/devices/{id}/refresh` - Refresh device info

### Streaming
- `GET /api/devices/{id}/stream/{profileToken}` - Get stream URI
- `POST /api/devices/{id}/webrtc` - Start WebRTC streaming
- `GET /api/devices/{id}/snapshot/{profileToken}` - Get camera snapshot

### PTZ Control
- `POST /api/devices/{id}/ptz/move` - Continuous PTZ movement
- `POST /api/devices/{id}/ptz/stop` - Stop PTZ movement
- `GET /api/devices/{id}/ptz/presets` - Get PTZ presets
- `POST /api/devices/{id}/ptz/goto` - Go to preset

## Architecture

```
├── cmd/
│   └── server/
│       ├── main.go          # HTTP server and API handlers
│       └── web/             # Embedded web files
│           ├── index.html
│           └── static/
│               ├── css/
│               └── js/
├── internal/
│   ├── onvif/
│   │   ├── client.go        # ONVIF SOAP client
│   │   └── discovery.go     # WS-Discovery implementation
│   └── webrtc/
│       └── bridge.go        # RTSP to WebRTC bridge
└── go.mod
```

## Technology Stack

- **Backend**: Go with standard library HTTP server
- **ONVIF**: Custom SOAP client implementation
- **Streaming**: [gortsplib](https://github.com/bluenviron/gortsplib) + [pion/webrtc](https://github.com/pion/webrtc)
- **Frontend**: Vanilla JavaScript with modern CSS

## Supported ONVIF Features

- Device Management
  - GetDeviceInformation
  - GetServices
  - GetCapabilities
- Media
  - GetProfiles
  - GetStreamUri
  - GetSnapshotUri
- PTZ
  - ContinuousMove
  - Stop
  - GetPresets
  - GotoPreset

## Browser Compatibility

The web interface works in all modern browsers that support WebRTC:
- Chrome/Edge 79+
- Firefox 72+
- Safari 14.1+

## License

MIT License - See LICENSE for details

## Credits

- Original project: [ONVIF Device Manager](https://github.com/dxball/ONVIF-Device-Manager)
- RTSP library: [gortsplib](https://github.com/bluenviron/gortsplib)
- WebRTC library: [pion/webrtc](https://github.com/pion/webrtc)

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.
