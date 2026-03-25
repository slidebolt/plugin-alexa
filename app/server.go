package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	translate "github.com/slidebolt/plugin-alexa/internal/translate"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const (
	keepaliveInterval = 4 * time.Minute
	reconnectDelay    = 5 * time.Second
	writeTimeout      = 10 * time.Second
	readTimeout       = 10 * time.Minute // generous; keepalive resets it
)

// --- Wire types (AWS relay protocol) ---

// relayRequest is the envelope for messages sent TO the AWS relay.
type relayRequest struct {
	Action   string              `json:"action"`
	ClientID string              `json:"clientId,omitempty"`
	Secret   string              `json:"secret,omitempty"`
	Endpoint *translate.AlexaEndpoint `json:"endpoint,omitempty"`
	State    map[string]any      `json:"state,omitempty"`
	DeviceID string              `json:"deviceId,omitempty"`
}

// relayResponse is the envelope for messages received FROM the AWS relay.
type relayResponse struct {
	OK           *bool          `json:"ok,omitempty"`
	Error        string         `json:"error,omitempty"`
	ConnectionID string         `json:"connectionId,omitempty"`
	DeviceID     string         `json:"deviceId,omitempty"`
	Devices      []any          `json:"devices,omitempty"`
	Type         string         `json:"type,omitempty"`         // "alexaDirective" for pushed directives
	Directive    *awsDirective  `json:"directive,omitempty"`
}

// awsDirective is an Alexa directive pushed by the SmartHome Lambda.
type awsDirective struct {
	Header   awsDirectiveHeader `json:"header"`
	Endpoint awsEndpoint        `json:"endpoint"`
	Payload  map[string]any     `json:"payload"`
}

type awsDirectiveHeader struct {
	Namespace        string `json:"namespace"`
	Name             string `json:"name"`
	MessageID        string `json:"messageId"`
	Instance         string `json:"instance,omitempty"`
	CorrelationToken string `json:"correlationToken,omitempty"`
}

type awsEndpoint struct {
	EndpointID string `json:"endpointId"`
}

// --- alexaClient ---

type alexaClient struct {
	cfg   Config
	store storage.Storage
	cmds  *messenger.Commands

	mu   sync.Mutex
	conn *websocket.Conn

	stopCh chan struct{}
	doneCh chan struct{}

	// testMode disables reconnect loop for testing.
	testMode bool
}

func newClient(cfg Config, store storage.Storage, cmds *messenger.Commands) *alexaClient {
	return &alexaClient{
		cfg:    cfg,
		store:  store,
		cmds:   cmds,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins the connection loop in a background goroutine.
func (c *alexaClient) Start() error {
	if c.cfg.WSURL == "" || c.cfg.ClientID == "" || c.cfg.ClientSecret == "" || c.cfg.RelayToken == "" {
		return fmt.Errorf("missing ALEXA_WS_URL, ALEXA_CLIENT_ID, ALEXA_CLIENT_SECRET, or ALEXA_RELAY_TOKEN")
	}
	go c.connectLoop()
	return nil
}

// Stop gracefully shuts down the client.
func (c *alexaClient) Stop() {
	close(c.stopCh)
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	<-c.doneCh
}

// BroadcastChangeReport translates an entity state change into an AWS
// state_update and sends it over the active connection.
func (c *alexaClient) BroadcastChangeReport(entity domain.Entity) {
	props := translate.AlexaState(entity)
	if len(props) == 0 {
		return
	}

	state := alexaStatePayload(props)
	log.Printf("plugin-alexa: state_update %s (%d properties)", entity.Key(), len(props))
	c.send(relayRequest{
		Action:   "state_update",
		ClientID: c.cfg.ClientID,
		DeviceID: entity.Key(),
		State:    state,
	})
}

// UpsertDevice pushes a device_upsert for an entity.
func (c *alexaClient) UpsertDevice(entity domain.Entity) {
	ep := translate.ToAlexa(entity)
	props := translate.AlexaState(entity)
	state := alexaStatePayload(props)

	log.Printf("plugin-alexa: device_upsert %s (%s)", entity.Key(), entity.Name)
	c.send(relayRequest{
		Action:   "device_upsert",
		ClientID: c.cfg.ClientID,
		Endpoint: &ep,
		State:    state,
	})
}

// DeleteDevice pushes a device_delete for an entity.
func (c *alexaClient) DeleteDevice(entity domain.Entity) {
	c.send(relayRequest{
		Action:   "device_delete",
		ClientID: c.cfg.ClientID,
		DeviceID: entity.Key(),
	})
}

// --- connection lifecycle ---

func (c *alexaClient) connectLoop() {
	defer close(c.doneCh)

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		if err := c.connectAndRun(); err != nil {
			log.Printf("plugin-alexa: connection error: %v", err)
		}

		if c.testMode {
			return
		}

		select {
		case <-c.stopCh:
			return
		case <-time.After(reconnectDelay):
			log.Printf("plugin-alexa: reconnecting...")
		}
	}
}

func (c *alexaClient) connectAndRun() error {
	// 1. Dial the AWS WebSocket with the relay token.
	u, err := url.Parse(c.cfg.WSURL)
	if err != nil {
		return fmt.Errorf("parse ws url: %w", err)
	}
	q := u.Query()
	q.Set("connectToken", c.cfg.RelayToken)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		conn.Close()
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	log.Printf("plugin-alexa: connected to %s", c.cfg.WSURL)

	// 2. Register with clientId + secret.
	if err := c.register(conn); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	log.Printf("plugin-alexa: registered as %s", c.cfg.ClientID)

	// 3. Sync all PluginAlexa-labeled entities as device_upserts.
	if err := c.syncDevices(conn); err != nil {
		log.Printf("plugin-alexa: sync devices: %v", err)
		// Non-fatal — continue with directive loop.
	}

	// 4. Start keepalive ticker.
	stopKeepalive := make(chan struct{})
	go c.keepaliveLoop(conn, stopKeepalive)
	defer close(stopKeepalive)

	// 5. Read loop — listen for pushed directives from AWS.
	return c.readLoop(conn)
}

func (c *alexaClient) register(conn *websocket.Conn) error {
	msg := relayRequest{
		Action:   "register",
		ClientID: c.cfg.ClientID,
		Secret:   c.cfg.ClientSecret,
	}
	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	var resp relayResponse
	if err := conn.ReadJSON(&resp); err != nil {
		return fmt.Errorf("read register response: %w", err)
	}
	if resp.OK == nil || !*resp.OK {
		return fmt.Errorf("register rejected: %s", resp.Error)
	}
	return nil
}

func (c *alexaClient) syncDevices(conn *websocket.Conn) error {
	if c.store == nil {
		return nil
	}
	entries, err := c.store.Query(storage.Query{
		Where: []storage.Filter{{Field: "labels.PluginAlexa", Op: storage.Exists}},
	})
	if err != nil {
		return fmt.Errorf("query entities: %w", err)
	}

	for _, entry := range entries {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			continue
		}
		ep := translate.ToAlexa(entity)
		props := translate.AlexaState(entity)
		state := alexaStatePayload(props)

		msg := relayRequest{
			Action:   "device_upsert",
			ClientID: c.cfg.ClientID,
			Endpoint: &ep,
			State:    state,
		}
		conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err := conn.WriteJSON(msg); err != nil {
			return fmt.Errorf("upsert %s: %w", entity.Key(), err)
		}

		// Read ack (relay responds to every action).
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		var resp relayResponse
		if err := conn.ReadJSON(&resp); err != nil {
			return fmt.Errorf("read upsert response: %w", err)
		}
		if resp.OK != nil && !*resp.OK {
			log.Printf("plugin-alexa: upsert %s rejected: %s", entity.Key(), resp.Error)
		}
	}

	log.Printf("plugin-alexa: synced %d devices to AWS", len(entries))
	return nil
}

func (c *alexaClient) keepaliveLoop(conn *websocket.Conn, stop chan struct{}) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			active := c.conn == conn
			c.mu.Unlock()
			if !active {
				return
			}
			c.send(relayRequest{Action: "keepalive"})
		}
	}
}

func (c *alexaClient) readLoop(conn *websocket.Conn) error {
	for {
		select {
		case <-c.stopCh:
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))
		var msg relayResponse
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		// The relay pushes alexaDirective messages when Alexa sends a control command.
		if msg.Type == "alexaDirective" && msg.Directive != nil {
			c.handleDirective(msg.Directive)
		}
		// Other messages (keepalive acks, state_update acks) are logged but not acted on.
	}
}

// --- directive handling ---

func (c *alexaClient) handleDirective(d *awsDirective) {
	endpointID := d.Endpoint.EndpointID
	log.Printf("plugin-alexa: directive %s.%s for %s", d.Header.Namespace, d.Header.Name, endpointID)

	entity, err := c.findEntityByEndpointID(endpointID)
	if err != nil {
		log.Printf("plugin-alexa: entity not found for %q: %v", endpointID, err)
		return
	}

	payload := d.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	if d.Header.Instance != "" {
		payload["_instance"] = d.Header.Instance
	}

	cmd, err := translate.FromAlexa(entity.Type, d.Header.Namespace, d.Header.Name, payload)
	if err != nil {
		log.Printf("plugin-alexa: fromAlexa %s/%s: %v", d.Header.Namespace, d.Header.Name, err)
		return
	}

	// Route the command to the entity's owning plugin via messenger.
	if actionCmd, ok := cmd.(messenger.Action); ok && c.cmds != nil {
		target := domain.EntityKey{Plugin: entity.Plugin, DeviceID: entity.DeviceID, ID: entity.ID}
		if err := c.cmds.Send(target, actionCmd); err != nil {
			log.Printf("plugin-alexa: route command to %s: %v", target.Key(), err)
		}
	}
}

func (c *alexaClient) findEntityByEndpointID(endpointID string) (domain.Entity, error) {
	if c.store == nil {
		return domain.Entity{}, fmt.Errorf("no store")
	}
	entries, err := c.store.Query(storage.Query{
		Where: []storage.Filter{{Field: "labels.PluginAlexa", Op: storage.Exists}},
	})
	if err != nil {
		return domain.Entity{}, fmt.Errorf("query: %w", err)
	}
	for _, entry := range entries {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			continue
		}
		if entity.Key() == endpointID {
			return entity, nil
		}
	}
	return domain.Entity{}, fmt.Errorf("entity %q not found", endpointID)
}

// --- send helper (thread-safe) ---

func (c *alexaClient) send(msg relayRequest) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return
	}

	conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := conn.WriteJSON(msg); err != nil {
		log.Printf("plugin-alexa: send %s: %v", msg.Action, err)
	}
}

// --- helpers ---

func alexaStatePayload(props []translate.AlexaProperty) map[string]any {
	arr := make([]map[string]any, len(props))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, p := range props {
		arr[i] = map[string]any{
			"namespace":                 p.Namespace,
			"name":                      p.Name,
			"value":                     p.Value,
			"timeOfSample":              now,
			"uncertaintyInMilliseconds": 500,
		}
		if p.Instance != "" {
			arr[i]["instance"] = p.Instance
		}
	}
	return map[string]any{"properties": arr}
}

// --- Test helpers ---

// ServerRunner is the interface used by tests to start/stop the client.
type ServerRunner interface {
	Start() error
	Stop()
	Connected() <-chan struct{}
}

type testAlexaClient struct {
	*alexaClient
	connectedCh chan struct{}
}

func (t *testAlexaClient) Connected() <-chan struct{} { return t.connectedCh }

// NewClientForTest creates a standalone alexaClient for testing.
// It connects to the given wsURL with the provided credentials.
func NewClientForTest(cfg Config) ServerRunner {
	cl := newClient(cfg, nil, nil)
	cl.testMode = true
	return &testAlexaClient{
		alexaClient: cl,
		connectedCh: make(chan struct{}),
	}
}
