package main

import (
	"time"

	"github.com/sickplanet/onvif2go/internal/onvif"
)

// Device represents a saved ONVIF device
type Device struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Endpoint             string            `json:"endpoint"`
	Username             string            `json:"username"`
	Password             string            `json:"-"`
	Info                 *onvif.DeviceInfo `json:"info,omitempty"`
	Profiles             []onvif.Profile   `json:"profiles,omitempty"`
	Connected            bool              `json:"connected"`
	LastSeen             time.Time         `json:"lastSeen"`
	IsPublic             bool              `json:"isPublic"`
	AllowPublicPTZ       bool              `json:"allowPublicPTZ"`
	SupportsEvents       bool              `json:"supportsEvents"`
	EventsEnabled        bool              `json:"eventsEnabled"`
	EventIntervalSeconds int               `json:"eventIntervalSeconds"`
	eventService         string            `json:"-"`
}

// User represents an application user
type User struct {
	Username       string   `json:"username"`
	PasswordHash   string   `json:"passwordHash"`
	IsAdmin        bool     `json:"isAdmin"`
	IsDefaultAdmin bool     `json:"isDefaultAdmin"`
	Cameras        []string `json:"cameras"`
	PTZAllowed     []string `json:"ptzAllowed"`
	RTSPAllowed    []string `json:"rtspAllowed"`
}

// UserResponse represents a user without password hash
type UserResponse struct {
	Username       string   `json:"username"`
	IsAdmin        bool     `json:"isAdmin"`
	IsDefaultAdmin bool     `json:"isDefaultAdmin"`
	Cameras        []string `json:"cameras"`
	PTZAllowed     []string `json:"ptzAllowed"`
	RTSPAllowed    []string `json:"rtspAllowed"`
}

// Session represents a user session
type Session struct {
	Token     string
	Username  string
	ExpiresAt time.Time
}

// APIError represents an error response
type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
