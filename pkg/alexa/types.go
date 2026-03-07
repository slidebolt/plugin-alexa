// Package alexa provides core Alexa Smart Home Skill integration logic
// independent of the Slidebolt SDK wiring.
package alexa

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Logger is the logging interface the relay uses.
type Logger interface {
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
}

// DefaultLogger provides basic logging to stdout.
type DefaultLogger struct{}

func (l DefaultLogger) Info(format string, args ...interface{}) {
	println("[INFO] "+format, args)
}
func (l DefaultLogger) Warn(format string, args ...interface{}) {
	println("[WARN] "+format, args)
}
func (l DefaultLogger) Error(format string, args ...interface{}) {
	println("[ERROR] "+format, args)
}

// RelayClient manages the WebSocket connection to the Alexa relay service.
type RelayClient struct {
	endpoint    string
	secret      string
	clientID    string
	logger      Logger
	onMessage   func(map[string]any)
	onConnected func()

	connected atomic.Bool

	mu      sync.Mutex
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// Constants for WebSocket timeouts
const (
	WSWriteTimeout  = 5 * time.Second
	WSPongWait      = 5 * time.Minute
	WSPingPeriod    = 2 * time.Minute
	RegisterAckWait = 10 * time.Second
)

// AlexaDeviceProxy maps an Alexa endpoint to a Slidebolt device/entity
type AlexaDeviceProxy struct {
	TargetPluginID string `json:"target_plugin_id"`
	TargetDeviceID string `json:"target_device_id"`
	TargetEntityID string `json:"target_entity_id"`
}
