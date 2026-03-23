package app

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	translate "github.com/slidebolt/plugin-alexa/internal/translate"
	storage "github.com/slidebolt/sb-storage-sdk"
)

// --- Wire types (Alexa relay protocol) ---

// wireMessage is the envelope for all messages between plugin and relay.
type wireMessage struct {
	Type string `json:"type"`

	// hello handshake
	Auth *bool `json:"auth,omitempty"`

	// discovery response
	Endpoints []translate.AlexaEndpoint `json:"endpoints,omitempty"`

	// directive (relay → plugin)
	Directive *wireDirective `json:"directive,omitempty"`

	// directive_result (plugin → relay)
	ID      string `json:"id,omitempty"`
	Success *bool  `json:"success,omitempty"`

	// change_report (plugin → relay)
	Event *wireAlexaEvent `json:"event,omitempty"`
}

// wireDirective represents an Alexa directive arriving from the relay.
type wireDirective struct {
	Header   wireHeader     `json:"header"`
	Endpoint wireEndpoint   `json:"endpoint"`
	Payload  map[string]any `json:"payload"`
}

type wireHeader struct {
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	MessageID  string `json:"messageId"`
	Instance   string `json:"instance,omitempty"`
}

type wireEndpoint struct {
	EndpointID string `json:"endpointId"`
}

// wireAlexaEvent wraps a fully-formed Alexa event (ChangeReport, StateReport).
type wireAlexaEvent struct {
	Header     wireHeader            `json:"header"`
	Endpoint   wireEndpoint          `json:"endpoint"`
	Payload    map[string]any        `json:"payload"`
}

// --- alexaServer ---

type alexaServer struct {
	cfg   Config
	store storage.Storage
	cmds  *messenger.Commands

	mu      sync.RWMutex
	clients []*websocket.Conn

	srv  *http.Server
	ln   net.Listener
	mdns *zeroconf.Server

	// onDiscoverySent is called after first successful handshake (test hook).
	onDiscoverySent func()

	// testEntities is an optional in-memory entity map for tests.
	testEntities map[string]domain.Entity
}

func newServer(cfg Config, store storage.Storage, cmds *messenger.Commands) *alexaServer {
	return &alexaServer{
		cfg:   cfg,
		store: store,
		cmds:  cmds,
	}
}

// Start begins listening for WebSocket connections and registers mDNS.
func (s *alexaServer) Start() (int, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1) //nolint:errcheck
			})
		},
	}
	ln, err := lc.Listen(context.Background(), "tcp", ":"+s.cfg.Port)
	if err != nil {
		return 0, fmt.Errorf("listen: %w", err)
	}
	s.ln = ln
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	s.srv = &http.Server{Handler: mux}

	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("plugin-alexa: server error: %v", err)
		}
	}()

	iface := outboundInterface()
	instanceID := stableInstanceID(iface)
	mdns, err := zeroconf.Register(
		"SlideBolt-Alexa",
		"_alexa-slidebolt._tcp",
		"local.",
		port,
		[]string{"version=1.0.0", "id=" + instanceID},
		iface,
	)
	if err != nil {
		log.Printf("plugin-alexa: mDNS registration failed: %v", err)
	} else {
		s.mdns = mdns
		log.Printf("plugin-alexa: mDNS advertising _alexa-slidebolt._tcp on port %d", port)
	}

	return port, nil
}

// Stop shuts down the HTTP server and unregisters mDNS.
func (s *alexaServer) Stop() {
	s.mu.Lock()
	for _, c := range s.clients {
		c.Close()
	}
	s.clients = nil
	s.mu.Unlock()

	if s.mdns != nil {
		s.mdns.Shutdown()
	}
	if s.srv != nil {
		s.srv.Shutdown(context.Background()) //nolint:errcheck
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *alexaServer) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("plugin-alexa: upgrade error: %v", err)
		return
	}
	defer func() {
		s.removeClient(conn)
		conn.Close()
	}()

	log.Printf("plugin-alexa: relay connected from %s", r.RemoteAddr)

	// 1. Read hello from relay
	var hello wireMessage
	if err := conn.ReadJSON(&hello); err != nil {
		log.Printf("plugin-alexa: read hello: %v", err)
		return
	}
	if hello.Type != "hello" {
		log.Printf("plugin-alexa: expected hello, got %q", hello.Type)
		return
	}

	// 2. Respond with hello + auth
	authOK := true
	if err := conn.WriteJSON(wireMessage{Type: "hello", Auth: &authOK}); err != nil {
		log.Printf("plugin-alexa: write hello: %v", err)
		return
	}

	// 3. Send discovery response with all Alexa endpoints
	endpoints, err := s.buildDiscovery()
	if err != nil {
		log.Printf("plugin-alexa: build discovery: %v", err)
		return
	}
	if err := conn.WriteJSON(wireMessage{Type: "discovery", Endpoints: endpoints}); err != nil {
		log.Printf("plugin-alexa: write discovery: %v", err)
		return
	}
	log.Printf("plugin-alexa: discovery sent (%d endpoints)", len(endpoints))

	if s.onDiscoverySent != nil {
		s.onDiscoverySent()
	}

	// 4. Register client and enter directive loop
	s.addClient(conn)
	for {
		var msg wireMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("plugin-alexa: relay disconnected: %v", err)
			return
		}

		if msg.Type == "directive" && msg.Directive != nil {
			s.handleDirective(conn, msg)
		} else {
			log.Printf("plugin-alexa: unknown message type %q", msg.Type)
		}
	}
}

// handleDirective processes an Alexa directive from the relay, translates it
// to a domain command, and routes it to the owning plugin.
func (s *alexaServer) handleDirective(conn *websocket.Conn, msg wireMessage) {
	d := msg.Directive
	endpointID := d.Endpoint.EndpointID

	entity, err := s.findEntityByEndpointID(endpointID)
	if err != nil {
		log.Printf("plugin-alexa: entity not found for %q: %v", endpointID, err)
		fail := false
		conn.WriteJSON(wireMessage{Type: "directive_result", ID: d.Header.MessageID, Success: &fail}) //nolint:errcheck
		return
	}

	// Pass instance from header into payload so FromAlexa can read it.
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
		fail := false
		conn.WriteJSON(wireMessage{Type: "directive_result", ID: d.Header.MessageID, Success: &fail}) //nolint:errcheck
		return
	}

	if s.testEntities != nil {
		// Test mode: apply locally and broadcast.
		updated := applyCommand(entity, cmd)
		s.testEntities[endpointID] = updated
		success := true
		conn.WriteJSON(wireMessage{Type: "directive_result", ID: d.Header.MessageID, Success: &success}) //nolint:errcheck
		s.BroadcastChangeReport(updated)
		return
	}

	// Production: route the command to the entity's owning plugin.
	if actionCmd, ok := cmd.(messenger.Action); ok && s.cmds != nil {
		target := domain.EntityKey{Plugin: entity.Plugin, DeviceID: entity.DeviceID, ID: entity.ID}
		if err := s.cmds.Send(target, actionCmd); err != nil {
			log.Printf("plugin-alexa: route command to %s: %v", target.Key(), err)
			fail := false
			conn.WriteJSON(wireMessage{Type: "directive_result", ID: d.Header.MessageID, Success: &fail}) //nolint:errcheck
			return
		}
	}

	success := true
	conn.WriteJSON(wireMessage{Type: "directive_result", ID: d.Header.MessageID, Success: &success}) //nolint:errcheck
}

// findEntityByEndpointID looks up an entity by its Alexa endpoint ID (entity key).
func (s *alexaServer) findEntityByEndpointID(endpointID string) (domain.Entity, error) {
	if s.testEntities != nil {
		if e, ok := s.testEntities[endpointID]; ok {
			return e, nil
		}
		return domain.Entity{}, fmt.Errorf("entity %q not found", endpointID)
	}
	entries, err := s.store.Query(storage.Query{
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

// BroadcastChangeReport sends an Alexa ChangeReport to all connected relay clients.
func (s *alexaServer) BroadcastChangeReport(entity domain.Entity) {
	props := translate.AlexaState(entity)
	changeProps := make([]translate.AlexaProperty, 0, len(props))
	for _, p := range props {
		if p.Namespace != "Alexa.EndpointHealth" {
			changeProps = append(changeProps, p)
		}
	}
	if len(changeProps) == 0 {
		return
	}

	event := &wireAlexaEvent{
		Header: wireHeader{
			Namespace: "Alexa",
			Name:      "ChangeReport",
		},
		Endpoint: wireEndpoint{
			EndpointID: entity.Key(),
		},
		Payload: map[string]any{
			"change": map[string]any{
				"cause":      map[string]string{"type": "PHYSICAL_INTERACTION"},
				"properties": changeProps,
			},
		},
	}

	msg := wireMessage{Type: "change_report", Event: event}
	s.Broadcast(msg)
}

// Broadcast sends a message to all connected relay clients.
func (s *alexaServer) Broadcast(msg wireMessage) {
	s.mu.RLock()
	clients := make([]*websocket.Conn, len(s.clients))
	copy(clients, s.clients)
	s.mu.RUnlock()

	for _, conn := range clients {
		if err := conn.WriteJSON(msg); err != nil {
			log.Printf("plugin-alexa: broadcast error: %v", err)
		}
	}
}

func (s *alexaServer) addClient(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients = append(s.clients, conn)
}

func (s *alexaServer) removeClient(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.clients {
		if c == conn {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return
		}
	}
}

// buildDiscovery returns all PluginAlexa-labeled entities as Alexa endpoints.
func (s *alexaServer) buildDiscovery() ([]translate.AlexaEndpoint, error) {
	if s.testEntities != nil {
		endpoints := make([]translate.AlexaEndpoint, 0, len(s.testEntities))
		for _, entity := range s.testEntities {
			endpoints = append(endpoints, translate.ToAlexa(entity))
		}
		return endpoints, nil
	}
	if s.store == nil {
		return []translate.AlexaEndpoint{}, nil
	}
	entries, err := s.store.Query(storage.Query{
		Where: []storage.Filter{{Field: "labels.PluginAlexa", Op: storage.Exists}},
	})
	if err != nil {
		return nil, fmt.Errorf("query entities: %w", err)
	}
	endpoints := make([]translate.AlexaEndpoint, 0, len(entries))
	for _, entry := range entries {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			continue
		}
		endpoints = append(endpoints, translate.ToAlexa(entity))
	}
	return endpoints, nil
}

// --- Network helpers ---

func stableInstanceID(ifaces []net.Interface) string {
	for _, iface := range ifaces {
		if len(iface.HardwareAddr) > 0 {
			return hex.EncodeToString(iface.HardwareAddr)
		}
	}
	if h, err := net.LookupAddr("127.0.0.1"); err == nil && len(h) > 0 {
		return h[0]
	}
	return "slidebolt-alexa-unknown"
}

func outboundInterface() []net.Interface {
	conn, err := net.Dial("udp", "1.1.1.1:53")
	if err != nil {
		return nil
	}
	defer conn.Close()
	localIP := conn.LocalAddr().(*net.UDPAddr).IP

	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.Equal(localIP) {
				return []net.Interface{iface}
			}
		}
	}
	return nil
}

// slugify converts a string to a safe ID: lowercase, non-alphanum replaced
// with underscores, leading/trailing underscores stripped.
func slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prev := byte('_')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteByte(c)
			prev = c
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 32)
			prev = c
		default:
			if prev != '_' {
				b.WriteByte('_')
				prev = '_'
			}
		}
	}
	out := b.String()
	return strings.Trim(out, "_")
}

// --- Test helpers ---

// ServerRunner is returned by NewServerForTest for testing.
type ServerRunner interface {
	Start() (int, error)
	Stop()
	Connected() <-chan struct{}
}

type testAlexaServer struct {
	*alexaServer
	connectedCh chan struct{}
	once        sync.Once
}

func (t *testAlexaServer) Connected() <-chan struct{} { return t.connectedCh }

func (t *testAlexaServer) Start() (int, error) {
	t.alexaServer.onDiscoverySent = func() {
		t.once.Do(func() { close(t.connectedCh) })
	}
	return t.alexaServer.Start()
}

// NewServerForTest creates a standalone alexaServer for testing.
func NewServerForTest(cfg Config, entities ...domain.Entity) ServerRunner {
	s := newServer(cfg, nil, nil)
	if len(entities) > 0 {
		s.testEntities = make(map[string]domain.Entity, len(entities))
		for _, e := range entities {
			s.testEntities[e.Key()] = e
		}
	}
	return &testAlexaServer{
		alexaServer: s,
		connectedCh: make(chan struct{}),
	}
}
