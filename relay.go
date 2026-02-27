package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	wsWriteTimeout  = 5 * time.Second
	wsPongWait      = 5 * time.Minute
	wsPingPeriod    = 2 * time.Minute
	registerAckWait = 10 * time.Second
)

// Logger is the logging interface the relay uses.
type Logger interface {
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
}

type DefaultLogger struct{}

func (l DefaultLogger) Info(format string, args ...interface{}) {
	fmt.Printf("[INFO] "+format+"\n", args...)
}
func (l DefaultLogger) Warn(format string, args ...interface{}) {
	fmt.Printf("[WARN] "+format+"\n", args...)
}
func (l DefaultLogger) Error(format string, args ...interface{}) {
	fmt.Printf("[ERROR] "+format+"\n", args...)
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

func NewRelayClient(
	endpoint, secret, clientID string,
	logger Logger,
	onMessage func(map[string]any),
	onConnected func(),
) *RelayClient {
	if logger == nil {
		logger = DefaultLogger{}
	}
	return &RelayClient{
		endpoint:    endpoint,
		secret:      secret,
		clientID:    clientID,
		logger:      logger,
		onMessage:   onMessage,
		onConnected: onConnected,
	}
}

func (r *RelayClient) IsConnected() bool {
	return r.connected.Load()
}

func (r *RelayClient) RunLoop(ctx context.Context) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if err := r.connectOnce(ctx); err != nil && ctx.Err() == nil {
			r.logger.Warn("[alexa-relay] disconnected: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (r *RelayClient) connectOnce(ctx context.Context) error {
	r.logger.Info("[alexa-relay] connecting to %s", redactConnectToken(r.endpoint))

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dialCancel()

	conn, resp, err := websocket.DefaultDialer.DialContext(dialCtx, r.endpoint, nil)
	if err != nil {
		status := "unknown"
		if resp != nil {
			status = resp.Status
		}
		return fmt.Errorf("dial failed (http=%s): %w", status, err)
	}

	conn.SetReadLimit(512 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))

	r.mu.Lock()
	r.conn = conn
	r.mu.Unlock()

	r.logger.Info("[alexa-relay] connected, sending register")
	if err := r.sendRegister(); err != nil {
		_ = conn.Close()
		r.mu.Lock()
		r.conn = nil
		r.mu.Unlock()
		return fmt.Errorf("register failed: %w", err)
	}

	if err := r.waitForRegisterAck(ctx, conn); err != nil {
		r.logger.Warn("[alexa-relay] register ack wait failed: %v", err)
		_ = conn.Close()
		r.mu.Lock()
		r.conn = nil
		r.mu.Unlock()
		return fmt.Errorf("register ack failed: %w", err)
	}

	r.connected.Store(true)
	r.logger.Info("[alexa-relay] registered, connection live")

	if r.onConnected != nil {
		go r.onConnected()
	}

	pingDone := make(chan struct{})
	go r.pingLoop(ctx, conn, pingDone)

	r.readLoop(ctx, conn)

	close(pingDone)
	r.connected.Store(false)

	r.mu.Lock()
	if r.conn == conn {
		r.conn = nil
	}
	r.mu.Unlock()
	_ = conn.Close()

	return fmt.Errorf("connection closed")
}

func (r *RelayClient) pingLoop(ctx context.Context, conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(wsPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			r.writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			_ = conn.WriteJSON(map[string]any{"action": "keepalive"})
			r.writeMu.Unlock()
		}
	}
}

func (r *RelayClient) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				r.logger.Warn("[alexa-relay] read error: %v", err)
			}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
		if msgType == websocket.TextMessage {
			var payload map[string]any
			if json.Unmarshal(msg, &payload) == nil && r.onMessage != nil {
				r.onMessage(payload)
			}
		}
	}
}

func (r *RelayClient) sendRegister() error {
	return r.writeJSON(map[string]string{
		"action":   "register",
		"secret":   r.secret,
		"clientId": r.clientID,
	})
}

func (r *RelayClient) waitForRegisterAck(ctx context.Context, conn *websocket.Conn) error {
	deadline := time.Now().Add(registerAckWait)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)
	defer func() {
		_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))
	}()

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if msgType != websocket.TextMessage {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		action, _ := payload["action"].(string)
		okVal, _ := payload["ok"].(bool)
		accepted, _ := payload["accepted"].(bool)
		errMsg, _ := payload["error"].(string)
		if action != "register" {
			if r.onMessage != nil {
				r.onMessage(payload)
			}
			continue
		}

		if okVal && accepted {
			return nil
		}
		if errMsg != "" {
			return errors.New(errMsg)
		}
		return fmt.Errorf("register rejected")
	}
}

func (r *RelayClient) WriteJSON(v any) error {
	if !r.connected.Load() {
		return fmt.Errorf("not registered")
	}
	return r.writeJSON(v)
}

func (r *RelayClient) writeJSON(v any) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return conn.WriteJSON(v)
}

func (r *RelayClient) Close() {
	r.mu.Lock()
	conn := r.conn
	r.conn = nil
	r.mu.Unlock()

	if conn != nil {
		r.writeMu.Lock()
		_ = conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"),
			time.Now().Add(wsWriteTimeout),
		)
		r.writeMu.Unlock()
		_ = conn.Close()
	}
	r.connected.Store(false)
}

func redactConnectToken(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := u.Query()
	if tok := q.Get("connectToken"); tok != "" {
		if len(tok) > 8 {
			q.Set("connectToken", tok[:4]+"..."+tok[len(tok)-4:])
		} else {
			q.Set("connectToken", "****")
		}
		u.RawQuery = q.Encode()
	}
	return u.String()
}
