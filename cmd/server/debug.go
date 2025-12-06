package main

import (
	"log"
	"os"
	"strings"
)

var debugEnabled bool

func debugLog(format string, args ...interface{}) {
	if !debugEnabled {
		return
	}
	log.Printf("[debug] "+format, args...)
}

func envBool(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return false
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
