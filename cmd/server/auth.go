package main

import (
	"encoding/json"
	"net/http"
)

// LoginRequest represents a login request
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse represents a login response
type LoginResponse struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

// FirstUserRequest represents the initial admin creation payload
type FirstUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin handles user login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	user := s.userStore.Get(req.Username)
	if user == nil || !checkPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "Invalid username or password")
		return
	}

	token := s.sessionStore.Create(user.Username)

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusOK, LoginResponse{
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
	})
}

// handleLogout handles user logout
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	token := getSessionToken(r)
	if token != "" {
		s.sessionStore.Delete(token)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns info about the current user
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if s.eventManager == nil {
		writeError(w, http.StatusInternalServerError, "Event manager not available")
		return
	}

	user := s.getCurrentUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	writeJSON(w, http.StatusOK, LoginResponse{
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
	})
}

// handleFirstUser provisions the initial administrator
func (s *Server) handleFirstUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if s.userStore.Count() > 0 {
		writeError(w, http.StatusForbidden, "Users already exist")
		return
	}

	var req FirstUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	user := &User{
		Username:       req.Username,
		PasswordHash:   hash,
		IsAdmin:        true,
		IsDefaultAdmin: true,
		Cameras:        []string{},
		PTZAllowed:     []string{},
	}

	if err := s.userStore.Create(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	token := s.sessionStore.Create(user.Username)
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	writeJSON(w, http.StatusCreated, LoginResponse{
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
	})
}

// handleNeedsSetup reports whether initial setup is required
func (s *Server) handleNeedsSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{
		"needsSetup": s.userStore.Count() == 0,
	})
}
