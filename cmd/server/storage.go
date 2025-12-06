package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	usersBucket   = []byte("users")
	devicesBucket = []byte("devices")
	configBucket  = []byte("config")
)

const (
	defaultDBPath   = "data/onvif.db"
	legacyUsersPath = "users.json"
)

// openDatabase initializes the BoltDB file and required buckets.
func openDatabase(path string) (*bolt.DB, error) {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}

	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(usersBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(devicesBucket); err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(configBucket); err != nil {
			return err
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrateLegacyUsers(db, legacyUsersPath); err != nil {
		log.Printf("[storage] legacy user migration failed: %v", err)
	}

	return db, nil
}

// migrateLegacyUsers imports the old JSON-based users if they exist and the DB is empty.
func migrateLegacyUsers(db *bolt.DB, legacyPath string) error {
	info, err := os.Stat(legacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}

	hasUsers := false
	if err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		if b == nil {
			return nil
		}
		hasUsers = b.Stats().KeyN > 0
		return nil
	}); err != nil {
		return err
	}
	if hasUsers {
		return nil
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}

	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		return err
	}
	if len(users) == 0 {
		return nil
	}

	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(usersBucket)
		for _, user := range users {
			if user == nil || user.Username == "" {
				continue
			}
			normalized := *user
			if normalized.Cameras == nil {
				normalized.Cameras = []string{}
			}
			if normalized.PTZAllowed == nil {
				normalized.PTZAllowed = []string{}
			}
			buf, err := json.Marshal(&normalized)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(normalized.Username), buf); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	backupPath := legacyPath + ".bak"
	if err := os.Rename(legacyPath, backupPath); err != nil {
		// Renaming is best-effort; log but do not fail
		log.Printf("[storage] unable to rename legacy users file: %v", err)
	}
	return nil
}

// deviceRecord is the persisted representation of a Device including internal fields.
type deviceRecord struct {
	Device
	Password     string `json:"password,omitempty"`
	EventService string `json:"eventService"`
}

func encodeDeviceRecord(device *Device) ([]byte, error) {
	record := deviceRecord{
		Device:       *device,
		Password:     device.Password,
		EventService: device.eventService,
	}
	return json.Marshal(record)
}

func decodeDeviceRecord(data []byte) (*Device, error) {
	var record deviceRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	device := record.Device
	device.Password = record.Password
	device.eventService = record.EventService
	if device.EventIntervalSeconds < 1 {
		device.EventIntervalSeconds = 1
	}
	return &device, nil
}
