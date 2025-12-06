package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sickplanet/onvif2go/internal/onvif"
)

// EventNotification represents a camera event pushed to clients
type EventNotification struct {
	DeviceID   string    `json:"deviceId"`
	DeviceName string    `json:"deviceName"`
	Topic      string    `json:"topic"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

// EventManager coordinates ONVIF event polling and SSE subscriptions
type EventManager struct {
	server  *Server
	mu      sync.Mutex
	workers map[string]*eventWorker
}

// NewEventManager creates a new EventManager instance
func NewEventManager(server *Server) *EventManager {
	return &EventManager{
		server:  server,
		workers: make(map[string]*eventWorker),
	}
}

// Enable ensures a device has a running event worker
func (m *EventManager) Enable(deviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.workers[deviceID]; exists {
		return
	}
	worker := newEventWorker(m.server, deviceID)
	m.workers[deviceID] = worker
	go worker.run()
}

// Disable stops the event worker for a device
func (m *EventManager) Disable(deviceID string) {
	m.mu.Lock()
	worker, exists := m.workers[deviceID]
	if exists {
		delete(m.workers, deviceID)
	}
	m.mu.Unlock()
	if exists {
		worker.stop()
		<-worker.doneCh
	}
}

// Subscribe registers a channel for SSE streaming
func (m *EventManager) Subscribe(deviceID string) (chan EventNotification, error) {
	m.mu.Lock()
	worker, exists := m.workers[deviceID]
	m.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("events not enabled for device")
	}
	ch := make(chan EventNotification, 10)
	worker.addSubscriber(ch)
	return ch, nil
}

// Unsubscribe removes a subscriber channel
func (m *EventManager) Unsubscribe(deviceID string, ch chan EventNotification) {
	m.mu.Lock()
	worker, exists := m.workers[deviceID]
	m.mu.Unlock()
	if exists {
		worker.removeSubscriber(ch)
	}
}

type eventWorker struct {
	server               *Server
	deviceID             string
	subscribers          map[chan EventNotification]struct{}
	subMu                sync.RWMutex
	stopOnce             sync.Once
	stopCh               chan struct{}
	doneCh               chan struct{}
	wakeCh               chan struct{}
	subscriptionURL      string
	subscriptionExpiry   time.Time
	lastNotificationTime time.Time
}

func newEventWorker(server *Server, deviceID string) *eventWorker {
	return &eventWorker{
		server:      server,
		deviceID:    deviceID,
		subscribers: make(map[chan EventNotification]struct{}),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		wakeCh:      make(chan struct{}, 1),
	}
}

func (w *eventWorker) run() {
	defer close(w.doneCh)
	for {
		if !w.hasSubscribers() {
			select {
			case <-w.stopCh:
				w.closeAllSubscribers()
				return
			case <-w.wakeCh:
				continue
			case <-time.After(5 * time.Second):
				continue
			}
		}

		select {
		case <-w.stopCh:
			w.closeAllSubscribers()
			return
		default:
		}

		device, ok := w.server.getDeviceSnapshot(w.deviceID)
		if !ok || !device.SupportsEvents || !device.EventsEnabled || device.eventService == "" {
			time.Sleep(5 * time.Second)
			continue
		}

		client := onvif.NewClient(device.Endpoint, device.Username, device.Password)
		if err := w.ensureSubscription(client, device.eventService); err != nil {
			log.Printf("[events] device %s subscription error: %v", device.Name, err)
			time.Sleep(5 * time.Second)
			continue
		}

		events, err := client.PullMessages(w.subscriptionURL, 15*time.Second, 10)
		if err != nil {
			log.Printf("[events] device %s pull error: %v", device.Name, err)
			w.subscriptionURL = ""
			time.Sleep(3 * time.Second)
			continue
		}

		if len(events) == 0 {
			continue
		}

		notifications := make([]EventNotification, 0, len(events))
		for _, evt := range events {
			timestamp := evt.UtcTime
			if timestamp.IsZero() {
				timestamp = time.Now().UTC()
			}
			notifications = append(notifications, EventNotification{
				DeviceID:   w.deviceID,
				DeviceName: device.Name,
				Topic:      evt.Topic,
				Message:    evt.Message,
				Timestamp:  timestamp,
			})
		}

		intervalSeconds := device.EventIntervalSeconds
		if intervalSeconds < 1 {
			intervalSeconds = 1
		}
		throttled := w.applyThrottle(notifications, time.Duration(intervalSeconds)*time.Second)
		if len(throttled) == 0 {
			continue
		}

		w.broadcast(throttled)
	}
}

func (w *eventWorker) ensureSubscription(client *onvif.Client, serviceURL string) error {
	if w.subscriptionURL != "" && time.Now().Before(w.subscriptionExpiry.Add(-30*time.Second)) {
		return nil
	}
	sub, err := client.CreatePullPointSubscription(serviceURL)
	if err != nil {
		return err
	}
	w.subscriptionURL = sub.ReferenceAddress
	if sub.TerminationTime.IsZero() {
		w.subscriptionExpiry = time.Now().Add(10 * time.Minute)
	} else {
		w.subscriptionExpiry = sub.TerminationTime
	}
	return nil
}

func (w *eventWorker) addSubscriber(ch chan EventNotification) {
	w.subMu.Lock()
	w.subscribers[ch] = struct{}{}
	w.subMu.Unlock()
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

func (w *eventWorker) removeSubscriber(ch chan EventNotification) {
	w.subMu.Lock()
	if _, exists := w.subscribers[ch]; exists {
		delete(w.subscribers, ch)
		close(ch)
	}
	w.subMu.Unlock()
}

func (w *eventWorker) closeAllSubscribers() {
	w.subMu.Lock()
	for ch := range w.subscribers {
		close(ch)
		delete(w.subscribers, ch)
	}
	w.subMu.Unlock()
}

func (w *eventWorker) broadcast(events []EventNotification) {
	w.subMu.RLock()
	defer w.subMu.RUnlock()
	for ch := range w.subscribers {
		for _, evt := range events {
			select {
			case ch <- evt:
			default:
			}
		}
	}
}

func (w *eventWorker) hasSubscribers() bool {
	w.subMu.RLock()
	defer w.subMu.RUnlock()
	return len(w.subscribers) > 0
}

func (w *eventWorker) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

func (w *eventWorker) applyThrottle(events []EventNotification, interval time.Duration) []EventNotification {
	if interval <= 0 {
		return events
	}
	filtered := make([]EventNotification, 0, len(events))
	for _, evt := range events {
		now := time.Now()
		if w.lastNotificationTime.IsZero() || now.Sub(w.lastNotificationTime) >= interval {
			filtered = append(filtered, evt)
			w.lastNotificationTime = now
		} else {
			debugLog("Suppressing event from %s due to %.0fs interval", evt.DeviceName, interval.Seconds())
		}
	}
	return filtered
}
