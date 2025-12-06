package onvif

import (
	"testing"
)

func TestValidateEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{
			name:     "valid http endpoint",
			endpoint: "http://192.168.1.100",
			wantErr:  false,
		},
		{
			name:     "valid https endpoint",
			endpoint: "https://192.168.1.100",
			wantErr:  false,
		},
		{
			name:     "empty endpoint",
			endpoint: "",
			wantErr:  true,
		},
		{
			name:     "invalid protocol",
			endpoint: "rtsp://192.168.1.100",
			wantErr:  true,
		},
		{
			name:     "no protocol",
			endpoint: "192.168.1.100",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEndpoint(tt.endpoint)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateEndpoint() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInjectCredentials(t *testing.T) {
	tests := []struct {
		name     string
		rtspURL  string
		username string
		password string
		want     string
		wantErr  bool
	}{
		{
			name:     "inject credentials",
			rtspURL:  "rtsp://192.168.1.100:554/stream",
			username: "admin",
			password: "password",
			want:     "rtsp://admin:password@192.168.1.100:554/stream",
			wantErr:  false,
		},
		{
			name:     "empty credentials",
			rtspURL:  "rtsp://192.168.1.100:554/stream",
			username: "",
			password: "",
			want:     "rtsp://192.168.1.100:554/stream",
			wantErr:  false,
		},
		{
			name:     "credentials with special chars",
			rtspURL:  "rtsp://192.168.1.100:554/stream",
			username: "admin",
			password: "pass@word",
			want:     "rtsp://admin:pass%40word@192.168.1.100:554/stream",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InjectCredentials(tt.rtspURL, tt.username, tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("InjectCredentials() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("InjectCredentials() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	client := NewClient("http://192.168.1.100", "admin", "password")
	if client == nil {
		t.Error("NewClient() returned nil")
	}
	if client.endpoint != "http://192.168.1.100" {
		t.Errorf("NewClient() endpoint = %v, want %v", client.endpoint, "http://192.168.1.100")
	}
	if client.username != "admin" {
		t.Errorf("NewClient() username = %v, want %v", client.username, "admin")
	}
}

func TestParseDeviceName(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		want   string
	}{
		{
			name:   "with name scope",
			scopes: "onvif://www.onvif.org/name/MyCamera onvif://www.onvif.org/hardware/Camera1",
			want:   "MyCamera",
		},
		{
			name:   "with hardware scope only",
			scopes: "onvif://www.onvif.org/hardware/Camera1",
			want:   "Camera1",
		},
		{
			name:   "empty scopes",
			scopes: "",
			want:   "Unknown Device",
		},
		{
			name:   "URL encoded name",
			scopes: "onvif://www.onvif.org/name/My%20Camera",
			want:   "My Camera",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDeviceName(tt.scopes)
			if got != tt.want {
				t.Errorf("parseDeviceName() = %v, want %v", got, tt.want)
			}
		})
	}
}
