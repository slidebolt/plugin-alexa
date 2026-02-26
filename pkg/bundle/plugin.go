package bundle

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	nats "github.com/nats-io/nats.go"
	sdk "github.com/slidebolt/plugin-sdk"

	"github.com/slidebolt/plugin-alexa/pkg/device"
	"github.com/slidebolt/plugin-alexa/pkg/logic"
)

const alexaNamePrefix = "XXX_"

// localDeviceSnapshot holds a point-in-time view of local alexa_devices,
// used to diff against the cloud list when a list_devices response arrives.
type localDeviceSnapshot struct {
	id   string
	name string
	sid  string
}

// AlexaPlugin is the top-level plugin struct.
type AlexaPlugin struct {
	bundle sdk.Bundle
	relay  *logic.RelayClient
	nc     *nats.Conn
	ctx    context.Context
	cancel context.CancelFunc
	wait   func()
	wg     sync.WaitGroup

	relayMu     sync.Mutex
	relayCancel context.CancelFunc
	relayCfg    relayConfig
	relayWg     sync.WaitGroup

	factory    *logic.AlexaEventFactory
	controlDev sdk.Device
	controlEnt sdk.Entity

	// pending list/clean operation — set by handleListDevices or handleCleanAlexa, consumed by handleRelayMessage
	compareMu            sync.Mutex
	pendingCompareActive bool
	pendingCompareLocal  []localDeviceSnapshot
	pendingClean         bool // if true, delete orphans instead of just logging

	deleteMu             sync.Mutex
	pendingDeletes       map[string]int
	deleteRetryScheduled bool

	outMu          sync.Mutex
	outCond        *sync.Cond
	outClosed      bool
	outMaxQueued   int
	outInterval    time.Duration
	outQueue       []outboundMsg
	outStateLatest map[string]outboundMsg
	outStateOrder  []string
	outStateQueued map[string]bool
	outListQueued  bool
}

type relayConfig struct {
	endpoint string
	secret   string
	clientID string
}

type outboundKind string

const (
	outboundKindGeneric     outboundKind = "generic"
	outboundKindState       outboundKind = "state_update"
	outboundKindList        outboundKind = "list_devices"
	outboundKindDeviceDel   outboundKind = "device_delete"
	defaultOutboundMax                   = 256
	defaultOutboundInterval              = 100 * time.Millisecond // 10 msgs/sec
)

type outboundMsg struct {
	kind     outboundKind
	deviceID string
	payload  any
}

func NewPlugin() *AlexaPlugin {
	p := &AlexaPlugin{
		pendingDeletes: make(map[string]int),
		factory:        logic.NewAlexaEventFactory(),
		outMaxQueued:   defaultOutboundMax,
		outInterval:    defaultOutboundInterval,
		outStateLatest: make(map[string]outboundMsg),
		outStateQueued: make(map[string]bool),
	}
	p.outCond = sync.NewCond(&p.outMu)
	return p
}

func (p *AlexaPlugin) Init(b sdk.Bundle) error {
	p.bundle = b
	_ = b.UpdateMetadata("Alexa Plugin")

	// Control device receives MCP commands (add_device / remove_device).
	// Always created, regardless of relay configuration.
	ctrl, ent, err := ensureAlexaControl(b)
	if err != nil {
		return err
	}
	ent.OnCommand(func(cmd string, payload map[string]interface{}) {
		switch strings.ToLower(cmd) {
		case "add_device":
			p.handleAddDevice(payload)
		case "remove_device":
			p.handleRemoveDevice(payload)
		case "update_device":
			p.handleUpdateDevice(payload)
		case "list_devices":
			p.handleListDevices()
		case "sync_devices":
			p.handleSyncDevices()
		case "clean_alexa":
			p.handleCleanAlexa()
		}
	})
	p.controlDev = ctrl
	p.controlEnt = ent
	b.Log().Info("[alexa] control ready: device=%s entity=%s", ctrl.ID(), ent.ID())

	cfg, err := p.resolveRelayConfig()
	if err != nil {
		_ = b.UpdateState("error: ALEXA_WS_ENDPOINT and ALEXA_CLIENT_SECRET are required")
		b.Log().Error("[alexa] init: ALEXA_WS_ENDPOINT and ALEXA_CLIENT_SECRET are required")
		return nil // non-fatal: plugin stays alive, waits for configure
	}

	b.Log().Info("[alexa] init: endpoint=%s clientId=%s", redactConnectToken(cfg.endpoint), cfg.clientID)

	// Direct NATS connection for entity state subscriptions (wildcard).
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		b.Log().Info("[alexa] NATS unavailable, state forwarding disabled: %v", err)
	} else {
		p.nc = nc
		p.subscribeNATS()
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.ctx = ctx
	p.cancel = cancel
	p.wait = p.wg.Wait

	p.startRelay(cfg)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.runOutboundSender(ctx)
	}()

	b.OnConfigure(p.reconfigure)

	_ = b.UpdateState("active")
	return nil
}

// RunCommand dispatches an MCP command directly, bypassing NATS.
// Useful for programmatic control and integration tests.
func (p *AlexaPlugin) RunCommand(cmd string, payload map[string]interface{}) {
	switch strings.ToLower(cmd) {
	case "add_device":
		p.handleAddDevice(payload)
	case "remove_device":
		p.handleRemoveDevice(payload)
	case "update_device":
		p.handleUpdateDevice(payload)
	case "list_devices":
		p.handleListDevices()
	case "sync_devices":
		p.handleSyncDevices()
	case "clean_alexa":
		p.handleCleanAlexa()
	}
}

// IsConnected reports whether the relay WebSocket is currently live.
func (p *AlexaPlugin) IsConnected() bool {
	if p.relay == nil {
		return false
	}
	return p.relay.IsConnected()
}

func (p *AlexaPlugin) Shutdown() {
	p.stopOutboundSender()
	p.stopRelay()
	if p.cancel != nil {
		p.cancel()
		p.wait()
	}
	if p.nc != nil {
		p.nc.Close()
	}
}

func (p *AlexaPlugin) stopOutboundSender() {
	p.outMu.Lock()
	p.outClosed = true
	p.outCond.Broadcast()
	p.outMu.Unlock()
}

// --- Control device setup ---

func ensureAlexaControl(b sdk.Bundle) (sdk.Device, sdk.Entity, error) {
	raw := b.Raw()
	if raw == nil {
		raw = make(map[string]interface{})
	}

	devID := sdk.UUID(asString(raw["control_device_uuid"]))
	entID := sdk.UUID(asString(raw["control_entity_uuid"]))

	var ctrl sdk.Device
	var ent sdk.Entity

	// 1. UUID lookup from persisted config.
	if devID != "" {
		if d, err := b.GetDevice(devID); err == nil {
			ctrl = d
		}
	}
	if entID != "" && ctrl != nil {
		if ents, err := ctrl.GetEntities(); err == nil {
			for _, e := range ents {
				if e.ID() == entID {
					ent = e
					break
				}
			}
		}
	}

	// 2. SourceID fallback.
	if ctrl == nil {
		if obj, ok := b.GetBySourceID(sdk.SourceID("alexa-control-device")); ok {
			if d, ok := obj.(sdk.Device); ok {
				ctrl = d
			}
		}
	}
	if ent == nil && ctrl != nil {
		if obj, ok := ctrl.GetBySourceID(sdk.SourceID("alexa-control-switch")); ok {
			if e, ok := obj.(sdk.Entity); ok {
				ent = e
			}
		}
	}

	// 3. Create if still missing.
	if ctrl == nil {
		created, err := b.CreateDevice()
		if err != nil {
			return nil, nil, err
		}
		ctrl = created
	}
	if ent == nil {
		created, err := ctrl.CreateEntity(sdk.TYPE_SWITCH)
		if err != nil {
			return nil, nil, err
		}
		ent = created
	}

	_ = ctrl.UpdateMetadata("Alexa Control", sdk.SourceID("alexa-control-device"))
	_ = ent.UpdateMetadata("Alexa Control", sdk.SourceID("alexa-control-switch"))

	// Persist UUIDs so lookup is O(1) on next restart.
	raw["control_device_uuid"] = string(ctrl.ID())
	raw["control_entity_uuid"] = string(ent.ID())
	_ = b.UpdateRaw(raw)

	return ctrl, ent, nil
}

// --- MCP command handlers ---

func (p *AlexaPlugin) handleAddDevice(payload map[string]interface{}) {
	if payload == nil {
		p.bundle.Log().Error("[alexa] add_device: nil payload")
		return
	}

	name := asString(payload["name"])
	if name == "" {
		p.bundle.Log().Error("[alexa] add_device: name is required")
		return
	}

	sid := asString(payload["source_id"])
	if sid == "" {
		sid = strings.ToLower(strings.ReplaceAll(name, " ", "-"))
	}

	entityTypeStr := strings.ToUpper(asString(payload["entity_type"]))
	entityType := sdk.TYPE_LIGHT
	if entityTypeStr == "SWITCH" {
		entityType = sdk.TYPE_SWITCH
	}

	caps := toStringSlice(payload["capabilities"])
	members := toStringSlice(payload["members"])

	dev, err := device.CreateAlexaDevice(p.bundle, name, sid, entityType, caps, members)
	if err != nil {
		p.bundle.Log().Error("[alexa] add_device failed: %v", err)
		return
	}
	p.bundle.Log().Info("[alexa] add_device: device=%s name=%s sid=%s type=%s caps=%v members=%d",
		dev.ID(), name, sid, entityType, caps, len(members))

	if p.relay != nil && p.relay.IsConnected() {
		p.sendDeviceUpsert(dev)
	}
}

func (p *AlexaPlugin) handleRemoveDevice(payload map[string]interface{}) {
	if payload == nil {
		p.bundle.Log().Error("[alexa] remove_device: nil payload")
		return
	}

	sid := asString(payload["source_id"])
	if sid == "" {
		p.bundle.Log().Error("[alexa] remove_device: source_id is required")
		return
	}

	obj, ok := p.bundle.GetBySourceID(sdk.SourceID(sid))
	if !ok {
		p.bundle.Log().Error("[alexa] remove_device: not found source_id=%s", sid)
		return
	}

	var dev sdk.Device
	switch v := obj.(type) {
	case sdk.Device:
		dev = v
	case sdk.Entity:
		if d, err := p.bundle.GetDevice(v.DeviceID()); err == nil {
			dev = d
		}
	}
	if dev == nil {
		p.bundle.Log().Error("[alexa] remove_device: could not resolve device source_id=%s", sid)
		return
	}

	p.bundle.Log().Info("[alexa] remove_device: device=%s source_id=%s", dev.ID(), sid)
	p.sendDeviceDelete(string(dev.ID()))
}

func (p *AlexaPlugin) handleUpdateDevice(payload map[string]interface{}) {
	if payload == nil {
		p.bundle.Log().Error("[alexa] update_device: nil payload")
		return
	}

	sid := asString(payload["source_id"])
	if sid == "" {
		p.bundle.Log().Error("[alexa] update_device: source_id is required")
		return
	}

	obj, ok := p.bundle.GetBySourceID(sdk.SourceID(sid))
	if !ok {
		p.bundle.Log().Error("[alexa] update_device: not found source_id=%s", sid)
		return
	}

	var dev sdk.Device
	switch v := obj.(type) {
	case sdk.Device:
		dev = v
	case sdk.Entity:
		if d, err := p.bundle.GetDevice(v.DeviceID()); err == nil {
			dev = d
		}
	}
	if dev == nil {
		p.bundle.Log().Error("[alexa] update_device: could not resolve device source_id=%s", sid)
		return
	}

	if name := asString(payload["name"]); name != "" {
		_ = dev.UpdateMetadata(name, sdk.SourceID(sid))
	}

	if members := toStringSlice(payload["members"]); len(members) > 0 {
		raw := dev.Raw()
		raw["members"] = members
		_ = dev.UpdateRaw(raw)
	}

	p.bundle.Log().Info("[alexa] update_device: device=%s source_id=%s", dev.ID(), sid)

	if p.relay != nil && p.relay.IsConnected() {
		p.sendDeviceUpsert(dev)
	}
}

// handleListDevices snapshots local alexa_devices, sends a list_devices request to
// the relay, then logs a comparison table when the response arrives.
func (p *AlexaPlugin) handleListDevices() {
	local := p.snapshotLocalDevices()

	p.bundle.Log().Info("[alexa] list_devices: local alexa_devices (%d):", len(local))
	for _, d := range local {
		p.bundle.Log().Info("[alexa]   local  id=%-38s name=%s source_id=%s", d.id, d.name, d.sid)
	}

	if p.relay == nil || !p.relay.IsConnected() {
		p.bundle.Log().Info("[alexa] list_devices: relay not connected, skipping cloud query")
		return
	}

	p.compareMu.Lock()
	p.pendingCompareActive = true
	p.pendingCompareLocal = local
	p.compareMu.Unlock()

	p.sendListDevices()
}

// handleSyncDevices force-upserts all local alexa_devices to the relay.
func (p *AlexaPlugin) handleSyncDevices() {
	if p.relay == nil || !p.relay.IsConnected() {
		p.bundle.Log().Info("[alexa] sync_devices: relay not connected")
		return
	}
	local := p.snapshotLocalDevices()
	p.bundle.Log().Info("[alexa] sync_devices: upserting %d local alexa_device(s)", len(local))
	p.upsertAllAlexaDevices()
}

// handleCleanAlexa requests the cloud device list and deletes any that have no
// corresponding local alexa_device.
func (p *AlexaPlugin) handleCleanAlexa() {
	if p.relay == nil || !p.relay.IsConnected() {
		p.bundle.Log().Info("[alexa] clean_alexa: relay not connected")
		return
	}

	local := p.snapshotLocalDevices()

	p.compareMu.Lock()
	p.pendingCompareActive = true
	p.pendingCompareLocal = local
	p.pendingClean = true
	p.compareMu.Unlock()

	p.bundle.Log().Info("[alexa] clean_alexa: requesting cloud device list (local=%d)", len(local))
	p.sendListDevices()
}

// snapshotLocalDevices returns a point-in-time list of all bundle devices tagged
// as alexa_device type.
func (p *AlexaPlugin) snapshotLocalDevices() []localDeviceSnapshot {
	var snap []localDeviceSnapshot
	for _, dev := range p.bundle.GetDevices() {
		raw := dev.Raw()
		if t, _ := raw["type"].(string); t != "alexa_device" {
			continue
		}
		meta := dev.Metadata()
		snap = append(snap, localDeviceSnapshot{
			id:   string(dev.ID()),
			name: meta.Name,
			sid:  string(meta.SourceID),
		})
	}
	return snap
}

// --- Configuration ---

func (p *AlexaPlugin) reconfigure() {
	cfg, err := p.resolveRelayConfig()
	if err != nil {
		p.bundle.Log().Info("[alexa] reconfigured: relay disabled (ALEXA_WS_ENDPOINT/ALEXA_CLIENT_SECRET missing)")
		p.stopRelay()
		return
	}

	p.relayMu.Lock()
	current := p.relayCfg
	p.relayMu.Unlock()
	if current == cfg {
		p.bundle.Log().Info("[alexa] reconfigured (no relay credential/endpoint changes)")
		return
	}

	p.bundle.Log().Info("[alexa] reconfigured: restarting relay endpoint=%s clientId=%s", redactConnectToken(cfg.endpoint), cfg.clientID)
	p.stopRelay()
	p.startRelay(cfg)
}

func (p *AlexaPlugin) resolveRelayConfig() (relayConfig, error) {
	cfg := p.bundle.Raw()
	endpoint, _ := cfg["ALEXA_WS_ENDPOINT"].(string)
	secret, _ := cfg["ALEXA_CLIENT_SECRET"].(string)
	clientID, _ := cfg["ALEXA_CLIENT_ID"].(string)
	connectToken, _ := cfg["RELAY_TOKEN"].(string)

	if endpoint == "" {
		endpoint = os.Getenv("ALEXA_WS_ENDPOINT")
	}
	if secret == "" {
		secret = os.Getenv("ALEXA_CLIENT_SECRET")
	}
	if clientID == "" {
		clientID = os.Getenv("ALEXA_CLIENT_ID")
	}
	if connectToken == "" {
		connectToken = os.Getenv("RELAY_TOKEN")
	}

	if endpoint == "" || secret == "" {
		return relayConfig{}, os.ErrInvalid
	}
	if connectToken != "" {
		withToken, err := appendConnectToken(endpoint, connectToken)
		if err != nil {
			return relayConfig{}, err
		}
		endpoint = withToken
	}
	return relayConfig{
		endpoint: endpoint,
		secret:   secret,
		clientID: clientID,
	}, nil
}

// --- NATS subscriptions ---

func (p *AlexaPlugin) subscribeNATS() {
	// Forward state changes from our virtual entities to the Alexa relay.
	p.nc.Subscribe("entity.*.state", func(msg *nats.Msg) {
		parts := strings.Split(msg.Subject, ".")
		if len(parts) != 3 {
			return
		}
		p.handleEntityState(parts[1], msg.Data)
	})
}

func (p *AlexaPlugin) handleEntityState(entityID string, data []byte) {
	for _, dev := range p.bundle.GetDevices() {
		raw := dev.Raw()
		if t, _ := raw["type"].(string); t != "alexa_device" {
			continue
		}
		ents, err := dev.GetEntities()
		if err != nil {
			continue
		}
		for _, e := range ents {
			if string(e.ID()) == entityID {
				p.forwardStateToAlexa(dev, data)
				return
			}
		}
	}
}

// forwardStateToAlexa parses an entity.*.state NATS message and sends it to the relay.
func (p *AlexaPlugin) forwardStateToAlexa(dev sdk.Device, data []byte) {
	var wrapper struct {
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil || len(wrapper.Payload) == 0 {
		return
	}
	state := stateFromProps(wrapper.Payload)
	if len(state) == 0 {
		return
	}
	p.enqueueRelayJSON(outboundKindState, string(dev.ID()), p.buildStatePayload(string(dev.ID()), state))
}

// --- Relay connection callback ---

func (p *AlexaPlugin) onRelayConnected() {
	_ = p.bundle.UpdateState("active: connected")
	p.upsertAllAlexaDevices()
	time.Sleep(1 * time.Second)
	p.sendListDevices()
}

func (p *AlexaPlugin) startRelay(cfg relayConfig) {
	if p.ctx == nil {
		return
	}
	relayCtx, relayCancel := context.WithCancel(p.ctx)
	relay := logic.NewRelayClient(
		cfg.endpoint, cfg.secret, cfg.clientID,
		&bundleLogger{p.bundle},
		p.handleRelayMessage,
		p.onRelayConnected,
	)

	p.relayMu.Lock()
	p.relay = relay
	p.relayCancel = relayCancel
	p.relayCfg = cfg
	p.relayMu.Unlock()

	p.wg.Add(1)
	p.relayWg.Add(1)
	go func(r *logic.RelayClient, ctx context.Context) {
		defer p.wg.Done()
		defer p.relayWg.Done()
		r.RunLoop(ctx)
	}(relay, relayCtx)
}

func (p *AlexaPlugin) stopRelay() {
	p.relayMu.Lock()
	relay := p.relay
	cancel := p.relayCancel
	p.relay = nil
	p.relayCancel = nil
	p.relayCfg = relayConfig{}
	p.relayMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if relay != nil {
		relay.Close()
	}
	p.relayWg.Wait()
}

func (p *AlexaPlugin) upsertAllAlexaDevices() {
	for _, dev := range p.bundle.GetDevices() {
		raw := dev.Raw()
		if t, _ := raw["type"].(string); t != "alexa_device" {
			continue
		}
		p.sendDeviceUpsert(dev)
		time.Sleep(50 * time.Millisecond)
	}
}

// --- Relay inbound message dispatch ---

func (p *AlexaPlugin) handleRelayMessage(payload map[string]any) {
	// Error responses.
	errMsg, _ := payload["error"].(string)
	if errMsg != "" {
		switch strings.ToLower(strings.TrimSpace(errMsg)) {
		case "unauthorized":
			p.bundle.Log().Info("[alexa] relay: unauthorized — forcing reconnect")
			if p.relay != nil {
				p.relay.Close()
			}
		case "internal server error", "internal server error.":
			p.schedulePendingDeleteRetry(1200 * time.Millisecond)
		case "rate limit exceeded":
			p.schedulePendingDeleteRetry(2500 * time.Millisecond)
		}
		return
	}

	// Delete acknowledgements.
	if ok, _ := payload["ok"].(bool); ok {
		if status, _ := payload["status"].(string); status == "deleted" {
			if deviceID, _ := payload["deviceId"].(string); deviceID != "" {
				p.clearPendingDelete(deviceID)
				p.removeLocalAlexaDevice(deviceID)
			}
		}
	}

	// Alexa directives.
	if msgType, _ := payload["type"].(string); msgType == "alexaDirective" {
		raw, _ := json.Marshal(payload)
		p.handleDirective(raw)
		return
	}

	// Device list response — may be a routine refresh or triggered by list_devices / clean_alexa.
	if cloudRaw, ok := payload["devices"].([]any); ok {
		p.compareMu.Lock()
		active := p.pendingCompareActive
		snap := p.pendingCompareLocal
		clean := p.pendingClean
		p.pendingCompareActive = false
		p.pendingCompareLocal = nil
		p.pendingClean = false
		p.compareMu.Unlock()

		if active {
			p.handleDeviceListResponse(snap, cloudRaw, clean)
		} else {
			p.bundle.Log().Info("[alexa] cloud device list: count=%d", len(cloudRaw))
		}
	}
}

func (p *AlexaPlugin) handleDirective(msg []byte) {
	var envelope struct {
		Directive json.RawMessage `json:"directive"`
	}
	if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope.Directive) == 0 {
		return
	}

	var d struct {
		Header struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		} `json:"header"`
		Endpoint struct {
			EndpointID string `json:"endpointId"`
		} `json:"endpoint"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(envelope.Directive, &d); err != nil {
		return
	}

	deviceID := strings.TrimSpace(d.Endpoint.EndpointID)
	if deviceID == "" {
		return
	}

	dev, err := p.bundle.GetDevice(sdk.UUID(deviceID))
	if err != nil {
		p.bundle.Log().Info("[alexa] directive for unknown device: %s", deviceID)
		return
	}

	raw := dev.Raw()
	if t, _ := raw["type"].(string); t != "alexa_device" {
		p.bundle.Log().Info("[alexa] directive for non-alexa device: %s", deviceID)
		return
	}

	ents, _ := dev.GetEntities()
	if len(ents) == 0 {
		return
	}
	ent := ents[0]
	entityID := string(ent.ID())

	switch d.Header.Namespace {
	case "Alexa.PowerController":
		on := d.Header.Name == "TurnOn"
		cmd := "TurnOff"
		if on {
			cmd = "TurnOn"
		}
		p.publishEntityCommand(entityID, map[string]interface{}{"command": cmd, "action": cmd})
		powerState := "OFF"
		if on {
			powerState = "ON"
		}
		p.enqueueRelayJSON(outboundKindState, deviceID, p.buildStatePayload(deviceID, stateFromPrimitive(map[string]any{"powerState": powerState})))

	case "Alexa.BrightnessController":
		brightness := coerceFloat(d.Payload["brightness"])
		if d.Header.Name == "AdjustBrightness" {
			delta := coerceFloat(d.Payload["brightnessDelta"])
			// Current brightness is stored in device Raw by the Lua fan-out script.
			if cur, ok := toFloat(dev.Raw()["brightness"]); ok {
				brightness = cur + delta
			}
		}
		brightness = clampFloat(brightness, 0, 100)
		p.publishEntityCommand(entityID, map[string]interface{}{
			"command": "SetBrightness", "action": "SetBrightness", "level": int(brightness),
		})
		p.enqueueRelayJSON(outboundKindState, deviceID, p.buildStatePayload(deviceID, stateFromPrimitive(map[string]any{"brightness": int(brightness)})))

	case "Alexa.ColorController":
		if d.Header.Name != "SetColor" {
			return
		}
		color := asMap(d.Payload["color"])
		hue := coerceFloat(color["hue"])
		sat := coerceFloat(color["saturation"])
		bri := coerceFloat(color["brightness"])
		r, g, b := hsvToRGB(hue, sat, bri)
		p.publishEntityCommand(entityID, map[string]interface{}{
			"command": "SetRGB", "action": "SetRGB", "r": r, "g": g, "b": b,
		})
		p.enqueueRelayJSON(outboundKindState, deviceID, p.buildStatePayload(deviceID, stateFromPrimitive(map[string]any{
			"color": map[string]any{"hue": hue, "saturation": sat, "brightness": bri},
		})))

	case "Alexa.ColorTemperatureController":
		if d.Header.Name != "SetColorTemperature" {
			return
		}
		kelvin := coerceFloat(d.Payload["colorTemperatureInKelvin"])
		if kelvin <= 0 {
			return
		}
		p.publishEntityCommand(entityID, map[string]interface{}{
			"command": "SetTemperature", "action": "SetTemperature", "kelvin": int(kelvin),
		})
		p.enqueueRelayJSON(outboundKindState, deviceID, p.buildStatePayload(deviceID, stateFromPrimitive(map[string]any{"colorTemperatureInKelvin": int(kelvin)})))
	}
}

// publishEntityCommand sends a command to an entity's NATS subject.
func (p *AlexaPlugin) publishEntityCommand(entityID string, payload map[string]interface{}) {
	if p.nc == nil {
		return
	}
	subject := "entity." + entityID + ".command"
	msg := map[string]interface{}{
		"source":    "plugin-alexa",
		"entity_id": entityID,
		"subject":   subject,
		"payload":   payload,
		"timestamp": time.Now().UnixNano(),
	}
	data, _ := json.Marshal(msg)
	_ = p.nc.Publish(subject, data)
}

// --- Outbound relay dispatcher ---

func (p *AlexaPlugin) runOutboundSender(ctx context.Context) {
	ticker := time.NewTicker(p.outInterval)
	defer ticker.Stop()

	for {
		msg, ok := p.popOutbound(ctx)
		if !ok {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if p.relay == nil {
			p.requeueOutbound(msg)
			continue
		}
		if err := p.relay.WriteJSON(msg.payload); err != nil {
			errText := strings.ToLower(err.Error())
			if strings.Contains(errText, "not registered") || strings.Contains(errText, "not connected") {
				p.requeueOutbound(msg)
				continue
			}
			p.bundle.Log().Info("[alexa] outbound send failed kind=%s device=%s: %v", msg.kind, msg.deviceID, err)
			if msg.kind == outboundKindDeviceDel {
				p.schedulePendingDeleteRetry(2 * time.Second)
			}
		}
	}
}

func (p *AlexaPlugin) popOutbound(ctx context.Context) (outboundMsg, bool) {
	p.outMu.Lock()
	defer p.outMu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return outboundMsg{}, false
		default:
		}

		if len(p.outQueue) > 0 {
			msg := p.outQueue[0]
			p.outQueue = p.outQueue[1:]
			if msg.kind == outboundKindList {
				p.outListQueued = false
			}
			p.outCond.Broadcast()
			return msg, true
		}

		for len(p.outStateOrder) > 0 {
			deviceID := p.outStateOrder[0]
			p.outStateOrder = p.outStateOrder[1:]
			msg, ok := p.outStateLatest[deviceID]
			delete(p.outStateQueued, deviceID)
			if !ok {
				continue
			}
			delete(p.outStateLatest, deviceID)
			p.outCond.Broadcast()
			return msg, true
		}

		if p.outClosed {
			return outboundMsg{}, false
		}
		p.outCond.Wait()
	}
}

func (p *AlexaPlugin) enqueueRelayJSON(kind outboundKind, deviceID string, payload any) {
	switch kind {
	case outboundKindState:
		p.enqueueState(outboundMsg{kind: kind, deviceID: deviceID, payload: payload})
	case outboundKindList:
		p.enqueueList(outboundMsg{kind: kind, deviceID: deviceID, payload: payload})
	default:
		p.enqueueRegular(outboundMsg{kind: kind, deviceID: deviceID, payload: payload})
	}
}

func (p *AlexaPlugin) requeueOutbound(msg outboundMsg) {
	switch msg.kind {
	case outboundKindState:
		p.enqueueState(msg)
	case outboundKindList:
		p.enqueueList(msg)
	default:
		p.outMu.Lock()
		defer p.outMu.Unlock()
		if p.outClosed {
			return
		}
		p.outQueue = append([]outboundMsg{msg}, p.outQueue...)
		p.outCond.Signal()
	}
}

func (p *AlexaPlugin) enqueueState(msg outboundMsg) {
	if msg.deviceID == "" {
		return
	}
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if p.outClosed {
		return
	}
	p.outStateLatest[msg.deviceID] = msg
	if !p.outStateQueued[msg.deviceID] {
		p.outStateQueued[msg.deviceID] = true
		p.outStateOrder = append(p.outStateOrder, msg.deviceID)
	}
	p.outCond.Signal()
}

func (p *AlexaPlugin) enqueueList(msg outboundMsg) {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if p.outClosed || p.outListQueued {
		return
	}
	for !p.outClosed && p.outQueuedCountLocked() >= p.outMaxQueued {
		p.outCond.Wait()
	}
	if p.outClosed {
		return
	}
	p.outListQueued = true
	p.outQueue = append(p.outQueue, msg)
	p.outCond.Signal()
}

func (p *AlexaPlugin) enqueueRegular(msg outboundMsg) {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	if p.outClosed {
		return
	}
	for !p.outClosed && p.outQueuedCountLocked() >= p.outMaxQueued {
		p.outCond.Wait()
	}
	if p.outClosed {
		return
	}
	p.outQueue = append(p.outQueue, msg)
	p.outCond.Signal()
}

func (p *AlexaPlugin) outQueuedCountLocked() int {
	return len(p.outQueue) + len(p.outStateLatest)
}

// --- Relay sends ---

func (p *AlexaPlugin) clientID() string {
	id, _ := p.bundle.Raw()["ALEXA_CLIENT_ID"].(string)
	if id == "" {
		id = os.Getenv("ALEXA_CLIENT_ID")
	}
	return id
}

func (p *AlexaPlugin) sendDeviceUpsert(dev sdk.Device) {
	endpoint := p.buildAlexaEndpoint(dev)
	if endpoint == nil {
		return
	}
	event := p.factory.CreateAddOrUpdateReport(endpoint)
	payload := map[string]any{
		"action":   "device_upsert",
		"clientId": p.clientID(),
		"endpoint": endpoint,
		"event":    event,
	}
	if state := p.stateFromDevice(dev); len(state) > 0 {
		payload["state"] = state
	}
	p.enqueueRelayJSON(outboundKindGeneric, string(dev.ID()), payload)
}

func (p *AlexaPlugin) sendStateUpdate(dev sdk.Device) {
	state := p.stateFromDevice(dev)
	if len(state) == 0 {
		return
	}
	p.enqueueRelayJSON(outboundKindState, string(dev.ID()), p.buildStatePayload(string(dev.ID()), state))
}

func (p *AlexaPlugin) buildStatePayload(deviceID string, state map[string]any) map[string]any {
	return map[string]any{
		"action":   "state_update",
		"clientId": p.clientID(),
		"deviceId": deviceID,
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"state":    state,
	}
}

func (p *AlexaPlugin) sendDeviceDelete(deviceID string) {
	p.trackPendingDelete(deviceID)
	event := p.factory.CreateDeleteReport(deviceID)
	payload := map[string]any{
		"action":   "device_delete",
		"clientId": p.clientID(),
		"deviceId": deviceID,
		"event":    event,
	}
	p.enqueueRelayJSON(outboundKindDeviceDel, deviceID, payload)
}

func (p *AlexaPlugin) sendListDevices() {
	p.enqueueRelayJSON(outboundKindList, "", map[string]string{
		"action":   "list_devices",
		"clientId": p.clientID(),
	})
}

// handleDeviceListResponse compares a local snapshot against the cloud list and
// logs a table. If clean=true, it also deletes cloud-only (orphan) devices.
func (p *AlexaPlugin) handleDeviceListResponse(local []localDeviceSnapshot, cloudRaw []any, clean bool) {
	// Build a set of local IDs for O(1) lookup.
	localByID := make(map[string]localDeviceSnapshot, len(local))
	for _, d := range local {
		localByID[d.id] = d
	}

	// Parse cloud devices.
	type cloudEntry struct {
		id   string
		name string
	}
	var cloud []cloudEntry
	for _, raw := range cloudRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["endpointId"].(string)
		name, _ := m["friendlyName"].(string)
		if id != "" {
			cloud = append(cloud, cloudEntry{id: id, name: name})
		}
	}

	cloudByID := make(map[string]cloudEntry, len(cloud))
	for _, c := range cloud {
		cloudByID[c.id] = c
	}

	// Log local devices with cloud status.
	p.bundle.Log().Info("[alexa] --- device list (local=%d, cloud=%d) ---", len(local), len(cloud))
	p.bundle.Log().Info("[alexa] LOCAL (devices asked to sync with Alexa):")
	if len(local) == 0 {
		p.bundle.Log().Info("[alexa]   (none)")
	}
	for _, d := range local {
		if _, inCloud := cloudByID[d.id]; inCloud {
			p.bundle.Log().Info("[alexa]   ✓ synced   id=%s name=%q source_id=%s", d.id, d.name, d.sid)
		} else {
			p.bundle.Log().Info("[alexa]   ✗ missing  id=%s name=%q source_id=%s  (not in Alexa cloud)", d.id, d.name, d.sid)
		}
	}

	// Log cloud devices with local status.
	p.bundle.Log().Info("[alexa] CLOUD (devices Alexa currently knows about):")
	if len(cloud) == 0 {
		p.bundle.Log().Info("[alexa]   (none)")
	}
	for _, c := range cloud {
		if _, inLocal := localByID[c.id]; inLocal {
			p.bundle.Log().Info("[alexa]   ✓ known    id=%s name=%q", c.id, c.name)
		} else {
			if clean {
				p.bundle.Log().Info("[alexa]   ✗ orphan   id=%s name=%q  → deleting", c.id, c.name)
				p.sendDeviceDelete(c.id)
			} else {
				p.bundle.Log().Info("[alexa]   ✗ orphan   id=%s name=%q  (not in local bundle)", c.id, c.name)
			}
		}
	}
	p.bundle.Log().Info("[alexa] ---")
}

// --- Pending delete tracking ---

func (p *AlexaPlugin) trackPendingDelete(deviceID string) {
	p.deleteMu.Lock()
	if _, ok := p.pendingDeletes[deviceID]; !ok {
		p.pendingDeletes[deviceID] = 0
	}
	p.deleteMu.Unlock()
}

func (p *AlexaPlugin) clearPendingDelete(deviceID string) {
	p.deleteMu.Lock()
	delete(p.pendingDeletes, deviceID)
	p.deleteMu.Unlock()
}

func (p *AlexaPlugin) removeLocalAlexaDevice(deviceID string) {
	dev, err := p.bundle.GetDevice(sdk.UUID(deviceID))
	if err != nil || dev == nil {
		return
	}
	if t, _ := dev.Raw()["type"].(string); t != "alexa_device" {
		p.bundle.Log().Info("[alexa] delete ack for non-local/non-alexa device id=%s (leaving local registry untouched)", deviceID)
		return
	}
	deleter, ok := p.bundle.(interface{ DeleteDevice(sdk.UUID) error })
	if !ok {
		p.bundle.Log().Error("[alexa] bundle does not support DeleteDevice; cannot remove local alexa device id=%s", deviceID)
		return
	}
	if err := deleter.DeleteDevice(sdk.UUID(deviceID)); err != nil {
		p.bundle.Log().Error("[alexa] failed to remove local alexa device after delete ack id=%s: %v", deviceID, err)
		return
	}
	p.bundle.Log().Info("[alexa] removed local alexa device after delete ack id=%s", deviceID)
}

func (p *AlexaPlugin) schedulePendingDeleteRetry(delay time.Duration) {
	p.deleteMu.Lock()
	if len(p.pendingDeletes) == 0 || p.deleteRetryScheduled {
		p.deleteMu.Unlock()
		return
	}
	p.deleteRetryScheduled = true
	p.deleteMu.Unlock()

	go func() {
		time.Sleep(delay)
		p.retryPendingDeletes()
		p.deleteMu.Lock()
		p.deleteRetryScheduled = false
		hasPending := len(p.pendingDeletes) > 0
		p.deleteMu.Unlock()
		if hasPending {
			p.schedulePendingDeleteRetry(3 * time.Second)
		}
	}()
}

func (p *AlexaPlugin) retryPendingDeletes() {
	p.deleteMu.Lock()
	ids := make([]string, 0, len(p.pendingDeletes))
	for id := range p.pendingDeletes {
		ids = append(ids, id)
		p.pendingDeletes[id]++
	}
	p.deleteMu.Unlock()
	for _, id := range ids {
		p.sendDeviceDelete(id)
	}
}

// --- Alexa endpoint building ---

func (p *AlexaPlugin) buildAlexaEndpoint(dev sdk.Device) map[string]any {
	ents, err := dev.GetEntities()
	if err != nil || len(ents) == 0 {
		return nil
	}
	primary := ents[0]
	meta := primary.Metadata()

	caps := make(map[string]bool)
	for _, c := range meta.Capabilities {
		caps[c] = true
	}

	display := "SWITCH"
	if meta.Type == sdk.TYPE_LIGHT || caps["brightness"] || caps["rgb"] || caps["temperature"] {
		display = "LIGHT"
	}

	devMeta := dev.Metadata()
	name := devMeta.Name
	if name == "" {
		name = string(dev.ID())
	}
	name = alexaNamePrefix + name

	alexaCaps := []map[string]any{
		{"type": "AlexaInterface", "interface": "Alexa", "version": "3"},
		{
			"type": "AlexaInterface", "interface": "Alexa.PowerController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "powerState"}}, "proactivelyReported": false, "retrievable": true,
			},
		},
	}
	if caps["brightness"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.BrightnessController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "brightness"}}, "proactivelyReported": false, "retrievable": true,
			},
		})
	}
	if caps["rgb"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.ColorController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "color"}}, "proactivelyReported": false, "retrievable": true,
			},
		})
	}
	if caps["temperature"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.ColorTemperatureController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "colorTemperatureInKelvin"}}, "proactivelyReported": false, "retrievable": true,
			},
		})
	}

	return map[string]any{
		"endpointId":        string(dev.ID()),
		"friendlyName":      name,
		"manufacturerName":  "SlideBolt",
		"description":       "SlideBolt device",
		"displayCategories": []string{display},
		"capabilities":      alexaCaps,
	}
}

// --- Alexa state translation ---

func (p *AlexaPlugin) stateFromDevice(dev sdk.Device) map[string]any {
	// The Lua fan-out script stores brightness/color/temperature in device Raw.
	raw := dev.Raw()
	props := map[string]interface{}{}
	if v, ok := raw["brightness"]; ok {
		props["brightness"] = v
	}
	if cr, ok := raw["color_rgb"].(map[string]interface{}); ok {
		props["r"] = cr["r"]
		props["g"] = cr["g"]
		props["b"] = cr["b"]
	}
	if v, ok := raw["color_temperature"]; ok {
		props["kelvin"] = v
	}
	if len(props) == 0 {
		return nil
	}
	return stateFromProps(props)
}

func stateFromProps(state map[string]interface{}) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any

	if v, ok := state["power"].(bool); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.PowerController", "name": "powerState",
			"value": boolToOnOff(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := toFloat(state["brightness"]); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.BrightnessController", "name": "brightness",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := toFloat(firstNonNil(state["temperature"], state["kelvin"])); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.ColorTemperatureController", "name": "colorTemperatureInKelvin",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if r, rok := toFloat(state["r"]); rok {
		if g, gok := toFloat(state["g"]); gok {
			if b, bok := toFloat(state["b"]); bok {
				h, s, bri := rgbToHSV(r/255.0, g/255.0, b/255.0)
				props = append(props, map[string]any{
					"namespace": "Alexa.ColorController", "name": "color",
					"value":        map[string]any{"hue": h, "saturation": s, "brightness": bri},
					"timeOfSample": ts, "uncertaintyInMilliseconds": 500,
				})
			}
		}
	}

	if len(props) == 0 {
		return nil
	}
	return map[string]any{"properties": props}
}

func stateFromPrimitive(vals map[string]any) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any
	if v, ok := vals["powerState"].(string); ok && v != "" {
		props = append(props, map[string]any{"namespace": "Alexa.PowerController", "name": "powerState", "value": v, "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := toFloat(vals["brightness"]); ok {
		props = append(props, map[string]any{"namespace": "Alexa.BrightnessController", "name": "brightness", "value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := toFloat(vals["colorTemperatureInKelvin"]); ok {
		props = append(props, map[string]any{"namespace": "Alexa.ColorTemperatureController", "name": "colorTemperatureInKelvin", "value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if c, ok := vals["color"].(map[string]any); ok {
		props = append(props, map[string]any{"namespace": "Alexa.ColorController", "name": "color", "value": c, "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if len(props) == 0 {
		return nil
	}
	return map[string]any{"properties": props}
}

// --- Math helpers ---

func rgbToHSV(r, g, b float64) (float64, float64, float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	delta := mx - mn
	var h float64
	switch {
	case delta == 0:
		h = 0
	case mx == r:
		h = 60 * math.Mod((g-b)/delta, 6)
	case mx == g:
		h = 60 * (((b - r) / delta) + 2)
	default:
		h = 60 * (((r - g) / delta) + 4)
	}
	if h < 0 {
		h += 360
	}
	var s float64
	if mx != 0 {
		s = delta / mx
	}
	return h, s, mx
}

func hsvToRGB(h, s, v float64) (int, int, int) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
	m := v - c
	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	clamp := func(v float64) int {
		i := int(math.Round(v * 255))
		if i < 0 {
			return 0
		}
		if i > 255 {
			return 255
		}
		return i
	}
	return clamp(r1 + m), clamp(g1 + m), clamp(b1 + m)
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func coerceFloat(v any) float64 {
	n, _ := toFloat(v)
	return n
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	}
	return 0, false
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func boolToOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func appendConnectToken(endpoint, token string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("connectToken", token)
	u.RawQuery = q.Encode()
	return u.String(), nil
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

func toStringSlice(v interface{}) []string {
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// bundleLogger adapts sdk.Logger to logic.Logger.
type bundleLogger struct{ b sdk.Bundle }

func (l *bundleLogger) Info(format string, args ...interface{}) {
	l.b.Log().Info("[inst:%s] "+format, append([]interface{}{ProcessInstanceID()}, args...)...)
}
func (l *bundleLogger) Warn(format string, args ...interface{}) {
	l.b.Log().Info("[inst:%s] [WARN] "+format, append([]interface{}{ProcessInstanceID()}, args...)...)
}
func (l *bundleLogger) Error(format string, args ...interface{}) {
	l.b.Log().Error("[inst:%s] "+format, append([]interface{}{ProcessInstanceID()}, args...)...)
}
