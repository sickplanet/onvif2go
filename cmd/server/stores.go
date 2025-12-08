package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	bolt "go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

// UserStore manages user persistence backed by BoltDB.
type UserStore struct {
	db *bolt.DB
}

// NewUserStore creates a new user store bound to the shared database.
func NewUserStore(db *bolt.DB) (*UserStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	return &UserStore{db: db}, nil
}

// Get returns a user by username.
func (s *UserStore) Get(username string) *User {
	if username == "" {
		return nil
	}
	var user *User
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b == nil {
			return nil
		}
		data := b.Get([]byte(username))
		if data == nil {
			return nil
		}
		decoded, err := decodeUser(data)
		if err != nil {
			log.Printf("[users] decode %s failed: %v", username, err)
			return nil
		}
		user = decoded
		return nil
	}); err != nil {
		log.Printf("[users] read %s failed: %v", username, err)
	}
	return user
}

// Create adds a new user.
func (s *UserStore) Create(user *User) error {
	if user == nil || user.Username == "" {
		return fmt.Errorf("invalid user payload")
	}
	normalizeUser(user)
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		key := []byte(user.Username)
		if existing := b.Get(key); existing != nil {
			return fmt.Errorf("user already exists")
		}
		data, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put(key, data)
	})
}

// Update modifies an existing user.
func (s *UserStore) Update(user *User) error {
	if user == nil || user.Username == "" {
		return fmt.Errorf("invalid user payload")
	}
	normalizeUser(user)
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		key := []byte(user.Username)
		if existing := b.Get(key); existing == nil {
			return fmt.Errorf("user not found")
		}
		data, err := json.Marshal(user)
		if err != nil {
			return err
		}
		return b.Put(key, data)
	})
}

// Delete removes a user.
func (s *UserStore) Delete(username string) error {
	if username == "" {
		return fmt.Errorf("user not found")
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		key := []byte(username)
		if existing := b.Get(key); existing == nil {
			return fmt.Errorf("user not found")
		}
		return b.Delete(key)
	})
}

// Count returns the number of stored users.
func (s *UserStore) Count() int {
	count := 0
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b != nil {
			count = b.Stats().KeyN
		}
		return nil
	}); err != nil {
		log.Printf("[users] count failed: %v", err)
	}
	return count
}

// All returns all users.
func (s *UserStore) All() []*User {
	users := make([]*User, 0)
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			u, err := decodeUser(v)
			if err != nil {
				log.Printf("[users] decode failed: %v", err)
				return nil
			}
			users = append(users, u)
			return nil
		})
	}); err != nil {
		log.Printf("[users] list failed: %v", err)
	}
	return users
}

// ReplaceDeviceRefs updates all users to replace an old device ID with a new one.
func (s *UserStore) ReplaceDeviceRefs(oldID, newID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			user, err := decodeUser(v)
			if err != nil {
				return nil // Skip malformed users
			}

			changed := false
			// Update Cameras list
			for i, camID := range user.Cameras {
				if camID == oldID {
					user.Cameras[i] = newID
					changed = true
				}
			}
			// Update PTZAllowed list
			for i, camID := range user.PTZAllowed {
				if camID == oldID {
					user.PTZAllowed[i] = newID
					changed = true
				}
			}

			if changed {
				data, err := json.Marshal(user)
				if err != nil {
					return err
				}
				return b.Put(k, data)
			}
			return nil
		})
	})
}

func decodeUser(data []byte) (*User, error) {
	var user User
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}
	normalizeUser(&user)
	return &user, nil
}

func normalizeUser(user *User) {
	if user.Cameras == nil {
		user.Cameras = []string{}
	}
	if user.PTZAllowed == nil {
		user.PTZAllowed = []string{}
	}
	if user.RTSPAllowed == nil {
		user.RTSPAllowed = []string{}
	}
}

// DeviceStore persists camera entries in BoltDB.
type DeviceStore struct {
	db *bolt.DB
}

// NewDeviceStore constructs a device store wrapper.
func NewDeviceStore(db *bolt.DB) *DeviceStore {
	return &DeviceStore{db: db}
}

// Save writes or updates a device.
func (s *DeviceStore) Save(device *Device) error {
	if device == nil || device.ID == "" {
		return fmt.Errorf("invalid device payload")
	}
	data, err := encodeDeviceRecord(device)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(devicesBucket).Put([]byte(device.ID), data)
	})
}

// Delete removes a device record from storage.
func (s *DeviceStore) Delete(id string) error {
	if id == "" {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(devicesBucket).Delete([]byte(id))
	})
}

// LoadAll returns every stored device keyed by ID.
func (s *DeviceStore) LoadAll() (map[string]*Device, error) {
	devices := make(map[string]*Device)
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(devicesBucket)
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			device, err := decodeDeviceRecord(v)
			if err != nil {
				return err
			}
			device.ID = string(k)
			devices[device.ID] = device
			return nil
		})
	}); err != nil {
		return nil, err
	}
	return devices, nil
}

// SessionStore manages user sessions in BoltDB.
type SessionStore struct {
	db *bolt.DB
}

// NewSessionStore creates a new session store.
func NewSessionStore(db *bolt.DB) *SessionStore {
	db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte("sessions"))
		return err
	})
	return &SessionStore{db: db}
}

// Create creates a new session for a user.
func (s *SessionStore) Create(username string) string {
	token := generateToken()
	session := &Session{
		Token:     token,
		Username:  username,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}

	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("sessions"))
		data, err := json.Marshal(session)
		if err != nil {
			return err
		}
		return b.Put([]byte(token), data)
	})

	if err != nil {
		log.Printf("Error creating session: %v", err)
		return ""
	}

	return token
}

// Get returns a session by token.
func (s *SessionStore) Get(token string) *Session {
	var session *Session
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("sessions"))
		data := b.Get([]byte(token))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &session)
	})

	if err != nil || session == nil {
		return nil
	}

	if time.Now().After(session.ExpiresAt) {
		s.Delete(token)
		return nil
	}

	return session
}

// Delete removes a session.
func (s *SessionStore) Delete(token string) {
	s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("sessions"))
		return b.Delete([]byte(token))
	})
}

// generateToken creates a random session token.
func generateToken() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Printf("Error generating token: %v", err)
	}
	return hex.EncodeToString(bytes)
}

// hashPassword hashes a password using bcrypt.
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// checkPassword verifies a password against a hash.
func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
