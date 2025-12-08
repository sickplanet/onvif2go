package main

import (
	"encoding/json"
	"net/http"
)

// CreateUserRequest represents a user creation request
type CreateUserRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	IsAdmin     bool     `json:"isAdmin"`
	Cameras     []string `json:"cameras"`
	PTZAllowed  []string `json:"ptzAllowed"`
	RTSPAllowed []string `json:"rtspAllowed"`
}

// UpdateUserRequest represents a user update request
type UpdateUserRequest struct {
	Password    *string  `json:"password,omitempty"`
	IsAdmin     *bool    `json:"isAdmin,omitempty"`
	Cameras     []string `json:"cameras,omitempty"`
	PTZAllowed  []string `json:"ptzAllowed,omitempty"`
	RTSPAllowed []string `json:"rtspAllowed,omitempty"`
}

// handleListUsers lists all users (admin only)
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	users := s.userStore.All()
	response := make([]UserResponse, len(users))
	for i, u := range users {
		response[i] = UserResponse{
			Username:       u.Username,
			IsAdmin:        u.IsAdmin,
			IsDefaultAdmin: u.IsDefaultAdmin,
			Cameras:        u.Cameras,
			PTZAllowed:     u.PTZAllowed,
			RTSPAllowed:    u.RTSPAllowed,
		}
	}

	writeJSON(w, http.StatusOK, response)
}

// handleCreateUser creates a new user (admin only)
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Username and password are required")
		return
	}

	isFirstUser := s.userStore.Count() == 0
	if isFirstUser {
		req.IsAdmin = true
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to hash password")
		return
	}

	cameras := req.Cameras
	if cameras == nil {
		cameras = []string{}
	}
	ptzAllowed := req.PTZAllowed
	if ptzAllowed == nil {
		ptzAllowed = []string{}
	}
	rtspAllowed := req.RTSPAllowed
	if rtspAllowed == nil {
		rtspAllowed = []string{}
	}
	if req.IsAdmin {
		cameras = []string{}
		ptzAllowed = []string{}
		rtspAllowed = []string{}
	}

	user := &User{
		Username:       req.Username,
		PasswordHash:   hash,
		IsAdmin:        req.IsAdmin,
		IsDefaultAdmin: isFirstUser,
		Cameras:        cameras,
		PTZAllowed:     ptzAllowed,
		RTSPAllowed:    rtspAllowed,
	}

	if err := s.userStore.Create(user); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, UserResponse{
		Username:       user.Username,
		IsAdmin:        user.IsAdmin,
		IsDefaultAdmin: user.IsDefaultAdmin,
		Cameras:        user.Cameras,
		PTZAllowed:     user.PTZAllowed,
		RTSPAllowed:    user.RTSPAllowed,
	})
}

// handleUpdateUser updates a user (admin only)
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := s.userStore.Get(username)
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if req.Password != nil && *req.Password != "" {
		hash, err := hashPassword(*req.Password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}
		user.PasswordHash = hash
	}

	if user.IsDefaultAdmin && req.IsAdmin != nil && !*req.IsAdmin {
		writeError(w, http.StatusBadRequest, "Primary administrator must remain an admin")
		return
	}

	if req.IsAdmin != nil {
		user.IsAdmin = *req.IsAdmin
	}

	if user.IsDefaultAdmin {
		user.IsAdmin = true
	}

	if user.IsAdmin {
		user.Cameras = []string{}
		user.PTZAllowed = []string{}
		user.RTSPAllowed = []string{}
	} else {
		if req.Cameras != nil {
			user.Cameras = req.Cameras
		}
		if req.PTZAllowed != nil {
			user.PTZAllowed = req.PTZAllowed
		}
		if req.RTSPAllowed != nil {
			user.RTSPAllowed = req.RTSPAllowed
		}
	}

	if err := s.userStore.Update(user); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, UserResponse{
		Username:       user.Username,
		IsAdmin:        user.IsAdmin,
		IsDefaultAdmin: user.IsDefaultAdmin,
		Cameras:        user.Cameras,
		PTZAllowed:     user.PTZAllowed,
		RTSPAllowed:    user.RTSPAllowed,
	})
}

// handleDeleteUser deletes a user (admin only)
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, username string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	currentUser := s.getCurrentUser(r)
	if currentUser != nil && currentUser.Username == username {
		writeError(w, http.StatusBadRequest, "Cannot delete your own account")
		return
	}

	user := s.userStore.Get(username)
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}
	if user.IsDefaultAdmin {
		writeError(w, http.StatusBadRequest, "Cannot delete the primary administrator")
		return
	}

	if err := s.userStore.Delete(username); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
