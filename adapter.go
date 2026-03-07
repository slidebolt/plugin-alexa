package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/slidebolt/plugin-alexa/pkg/alexa"
	"github.com/slidebolt/sdk-entities/light"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

// PluginAdapter implements the runner.Plugin interface using the alexa core package.
type PluginAdapter struct {
	config  runner.Config
	storage types.Storage
	mu      sync.RWMutex

	relay   *alexa.RelayClient
	factory *alexa.EventFactory
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// relayConnected tracks if the relay is currently connected
	relayConnected bool
}

// AlexaPluginConfig holds the plugin configuration
type AlexaPluginConfig struct {
	WSEndpoint   string `json:"ws_endpoint"`
	ClientSecret string `json:"client_secret"`
	ClientID     string `json:"client_id"`
	RelayToken   string `json:"relay_token"`
}

// OnInitialize initializes the plugin.
func (p *PluginAdapter) OnInitialize(config runner.Config, state types.Storage) (types.Manifest, types.Storage) {
	p.config = config
	p.storage = state
	p.factory = alexa.NewEventFactory()

	return types.Manifest{
		ID:      "plugin-alexa",
		Name:    "Alexa Bridge",
		Version: "1.1.0",
		Schemas: types.CoreDomains(),
	}, state
}

// OnReady starts the relay connection when the plugin is ready.
func (p *PluginAdapter) OnReady() {
	p.ctx, p.cancel = context.WithCancel(context.Background())

	cfg := p.resolveConfig()
	if cfg.WSEndpoint == "" || cfg.ClientSecret == "" {
		fmt.Println("[alexa] missing relay configuration, bridge inactive")
		return
	}

	p.relay = alexa.NewRelayClient(
		cfg.WSEndpoint, cfg.ClientSecret, cfg.ClientID,
		alexa.DefaultLogger{},
		p.handleRelayMessage,
		p.onRelayConnected,
	)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.relay.RunLoop(p.ctx)
	}()
}

// WaitReady waits for the plugin to be ready.
func (p *PluginAdapter) WaitReady(ctx context.Context) error {
	return nil
}

// OnShutdown shuts down the plugin.
func (p *PluginAdapter) OnShutdown() {
	if p.cancel != nil {
		p.cancel()
		p.wg.Wait()
	}
}

func (p *PluginAdapter) resolveConfig() AlexaPluginConfig {
	cfg := AlexaPluginConfig{
		WSEndpoint:   os.Getenv("ALEXA_WS_ENDPOINT"),
		ClientSecret: os.Getenv("ALEXA_CLIENT_SECRET"),
		ClientID:     os.Getenv("ALEXA_CLIENT_ID"),
		RelayToken:   os.Getenv("RELAY_TOKEN"),
	}

	if p.storage.Meta != "" {
		var sCfg AlexaPluginConfig
		if err := json.Unmarshal([]byte(p.storage.Meta), &sCfg); err == nil {
			if sCfg.WSEndpoint != "" {
				cfg.WSEndpoint = sCfg.WSEndpoint
			}
			if sCfg.ClientSecret != "" {
				cfg.ClientSecret = sCfg.ClientSecret
			}
			if sCfg.ClientID != "" {
				cfg.ClientID = sCfg.ClientID
			}
			if sCfg.RelayToken != "" {
				cfg.RelayToken = sCfg.RelayToken
			}
		}
	}

	if cfg.RelayToken != "" && cfg.WSEndpoint != "" {
		u, err := url.Parse(cfg.WSEndpoint)
		if err == nil {
			q := u.Query()
			q.Set("connectToken", cfg.RelayToken)
			u.RawQuery = q.Encode()
			cfg.WSEndpoint = u.String()
		}
	}

	return cfg
}

func (p *PluginAdapter) onRelayConnected() {
	p.mu.Lock()
	p.relayConnected = true
	p.mu.Unlock()
	fmt.Println("[alexa] relay connected")
}

func (p *PluginAdapter) handleRelayMessage(payload map[string]any) {
	if msgType, _ := payload["type"].(string); msgType == "alexaDirective" {
		if err := p.handleDirective(payload); err != nil {
			// Log the error - in a real implementation, we'd send an error response to Alexa
			fmt.Printf("[alexa] failed to handle directive: %v\n", err)
		}
	}
}

func (p *PluginAdapter) handleDirective(payload map[string]any) error {
	raw, _ := json.Marshal(payload)
	var envelope struct {
		Directive struct {
			Header struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"header"`
			Endpoint struct {
				EndpointID string `json:"endpointId"`
			} `json:"endpoint"`
			Payload map[string]any `json:"payload"`
		} `json:"directive"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("%w: failed to parse directive: %v", alexa.ErrInvalidCommand, err)
	}

	deviceID := envelope.Directive.Endpoint.EndpointID

	// Look up the Alexa proxy mapping from RawStore (storage.Data)
	var proxy alexa.AlexaDeviceProxy
	if len(p.storage.Data) > 0 {
		var data struct {
			Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
		}
		if err := json.Unmarshal(p.storage.Data, &data); err == nil && data.Devices != nil {
			if p, ok := data.Devices[deviceID]; ok {
				proxy = p
			} else {
				return fmt.Errorf("%w: directive for unknown device: %s", alexa.ErrNotFound, deviceID)
			}
		} else {
			return fmt.Errorf("%w: directive for unknown device: %s", alexa.ErrNotFound, deviceID)
		}
	} else {
		return fmt.Errorf("%w: directive for unknown device: %s", alexa.ErrNotFound, deviceID)
	}

	return p.forwardDirectiveToTarget(proxy, envelope.Directive.Header.Namespace, envelope.Directive.Header.Name, envelope.Directive.Payload)
}

func (p *PluginAdapter) forwardDirectiveToTarget(proxy alexa.AlexaDeviceProxy, namespace, name string, payload map[string]any) error {
	cmdPayload := map[string]any{}
	switch namespace {
	case "Alexa.PowerController":
		if name == "TurnOn" {
			cmdPayload["type"] = light.ActionTurnOn
		} else {
			cmdPayload["type"] = light.ActionTurnOff
		}
	case "Alexa.BrightnessController":
		if v, ok := payload["brightness"]; ok {
			cmdPayload["type"] = light.ActionSetBrightness
			cmdPayload["brightness"] = v
		}
	case "Alexa.ColorController":
		if name == "SetColor" {
			if color, ok := payload["color"].(map[string]any); ok {
				h, _ := alexa.ToFloat(color["hue"])
				s, _ := alexa.ToFloat(color["saturation"])
				v, _ := alexa.ToFloat(color["brightness"])
				r, g, b := alexa.HSVToRGB(h, s, v)
				cmdPayload["type"] = light.ActionSetRGB
				cmdPayload["rgb"] = []int{r, g, b}
			}
		}
	case "Alexa.ColorTemperatureController":
		if v, ok := payload["colorTemperatureInKelvin"]; ok {
			cmdPayload["type"] = light.ActionSetTemperature
			cmdPayload["temperature"] = v
		}
	case "Alexa.LockController":
		if name == "Lock" {
			cmdPayload["type"] = "lock"
		} else if name == "Unlock" {
			cmdPayload["type"] = "unlock"
			// Check for PIN in payload (for security)
			if pin, ok := payload["pin"].(string); ok && pin != "" {
				cmdPayload["pin"] = pin
			}
		}
	case "Alexa.ModeController":
		// Handle cover position changes
		if name == "SetMode" {
			if mode, ok := payload["mode"].(string); ok {
				switch mode {
				case "Position.Open":
					cmdPayload["type"] = "open_cover"
				case "Position.Closed":
					cmdPayload["type"] = "close_cover"
				default:
					cmdPayload["type"] = "set_cover_position"
					// Extract percentage from mode value if present
					cmdPayload["position"] = 50 // default to middle
				}
			}
		}
	}

	// We use the SDK's EventSink to emit an event representing the directive.
	// This event can be observed by other plugins or used to trigger automations.
	if p.config.EventSink == nil {
		return alexa.ErrRelayDisconnected
	}
	if _, ok := cmdPayload["type"].(string); !ok {
		return fmt.Errorf("%w: unsupported directive %s/%s", alexa.ErrInvalidCommand, namespace, name)
	}
	rawPayload, _ := json.Marshal(cmdPayload)
	err := p.config.EventSink.EmitEvent(types.InboundEvent{
		DeviceID: proxy.TargetDeviceID,
		EntityID: proxy.TargetEntityID,
		Payload:  json.RawMessage(rawPayload),
	})
	if err != nil {
		return fmt.Errorf("%w: failed to emit directive event: %v", alexa.ErrOffline, err)
	}

	return nil
}

// OnHealthCheck returns the health status of the plugin.
func (p *PluginAdapter) OnHealthCheck() (string, error) {
	return "perfect", nil
}

// OnStorageUpdate handles storage updates.
func (p *PluginAdapter) OnStorageUpdate(current types.Storage) (types.Storage, error) {
	// RawStore pattern: ensure storage.Data contains the Alexa proxy metadata
	// The plugin state is already in p.storage.Data from handleAddDevice
	// We need to merge it into the current storage being persisted
	p.mu.RLock()
	defer p.mu.RUnlock()

	current.Data = p.storage.Data
	return current, nil
}

// OnDeviceCreate handles device creation.
func (p *PluginAdapter) OnDeviceCreate(dev types.Device) (types.Device, error) { return dev, nil }

// OnDeviceUpdate handles device updates.
func (p *PluginAdapter) OnDeviceUpdate(dev types.Device) (types.Device, error) { return dev, nil }

// OnDeviceDelete handles device deletion.
func (p *PluginAdapter) OnDeviceDelete(id string) error { return nil }

// OnDevicesList lists all devices.
func (p *PluginAdapter) OnDevicesList(current []types.Device) ([]types.Device, error) {
	byID := make(map[string]types.Device, len(current)+2)
	for _, d := range current {
		byID[d.ID] = d
	}

	// Always include/refresh the control device
	byID["control"] = runner.ReconcileDevice(byID["control"], types.Device{
		ID:         "control",
		SourceID:   "alexa-control",
		SourceName: "Alexa Control",
	})

	// Read Alexa proxy devices from RawStore (storage.Data) - not from a shadow registry
	var alexaDevices map[string]alexa.AlexaDeviceProxy
	if len(p.storage.Data) > 0 {
		var data struct {
			Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
		}
		if err := json.Unmarshal(p.storage.Data, &data); err == nil && data.Devices != nil {
			alexaDevices = data.Devices
		}
	}

	for id := range alexaDevices {
		byID[id] = runner.ReconcileDevice(byID[id], types.Device{
			ID:         id,
			SourceID:   id,
			SourceName: "Alexa Proxy Device",
		})
	}

	out := make([]types.Device, 0, len(byID))
	for _, d := range byID {
		out = append(out, d)
	}
	return runner.EnsureCoreDevice("plugin-alexa", out), nil
}

// OnDeviceSearch handles device search.
func (p *PluginAdapter) OnDeviceSearch(q types.SearchQuery, res []types.Device) ([]types.Device, error) {
	return res, nil
}

// OnEntityCreate handles entity creation.
func (p *PluginAdapter) OnEntityCreate(e types.Entity) (types.Entity, error) { return e, nil }

// OnEntityUpdate handles entity updates.
func (p *PluginAdapter) OnEntityUpdate(e types.Entity) (types.Entity, error) { return e, nil }

// OnEntityDelete handles entity deletion.
func (p *PluginAdapter) OnEntityDelete(d, e string) error { return nil }

// OnEntitiesList lists all entities for a device.
func (p *PluginAdapter) OnEntitiesList(d string, c []types.Entity) ([]types.Entity, error) {
	if d == "control" {
		return []types.Entity{
			{
				ID:        "control",
				DeviceID:  "control",
				Domain:    "switch",
				LocalName: "Alexa Control",
			},
		}, nil
	}

	// Add availability entity for Alexa proxy devices
	// Check if this device is in our proxy list
	var alexaDevices map[string]alexa.AlexaDeviceProxy
	if len(p.storage.Data) > 0 {
		var data struct {
			Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
		}
		if err := json.Unmarshal(p.storage.Data, &data); err == nil && data.Devices != nil {
			alexaDevices = data.Devices
		}
	}

	if _, isAlexaDevice := alexaDevices[d]; isAlexaDevice {
		// Add availability entity for this device
		// Use "availability" as the ID (not device-specific) per task requirements
		availabilityEntity := types.Entity{
			ID:        "availability",
			DeviceID:  d,
			Domain:    "binary_sensor",
			LocalName: "Alexa Availability",
		}

		// Check if we already have an availability entity
		hasAvailability := false
		for _, e := range c {
			if e.ID == availabilityEntity.ID {
				hasAvailability = true
				break
			}
		}

		if !hasAvailability {
			c = append(c, availabilityEntity)
		}
	}

	return runner.EnsureCoreEntities("plugin-alexa", d, c), nil
}

// OnCommand handles commands for entities.
func (p *PluginAdapter) OnCommand(req types.Command, entity types.Entity) (types.Entity, error) {
	if entity.ID != "control" {
		return entity, nil
	}

	// Set sync_status to pending when processing a command
	entity = setSyncStatus(entity, alexa.SyncStatusPending)

	var payload map[string]any
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		// Return structured error and set sync_status to failed
		entity = setSyncStatus(entity, alexa.SyncStatusFailed)
		return entity, fmt.Errorf("%w: failed to unmarshal command: %v", alexa.ErrInvalidCommand, err)
	}

	action, _ := payload["type"].(string)
	if action == "add_device" {
		if err := p.handleAddDevice(payload); err != nil {
			entity = setSyncStatus(entity, alexa.SyncStatusFailed)
			return entity, err
		}
	}

	// Command processed successfully, set sync_status to synced
	entity = setSyncStatus(entity, alexa.SyncStatusSynced)
	return entity, nil
}

func (p *PluginAdapter) handleAddDevice(payload map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	id, _ := payload["id"].(string)
	if id == "" {
		return alexa.ErrInvalidCommand
	}

	proxy := alexa.AlexaDeviceProxy{
		TargetPluginID: asString(payload["target_plugin_id"]),
		TargetDeviceID: asString(payload["target_device_id"]),
		TargetEntityID: asString(payload["target_entity_id"]),
	}

	// Store in RawStore (storage.Data) instead of shadow registry
	var data struct {
		Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
	}
	if len(p.storage.Data) > 0 {
		json.Unmarshal(p.storage.Data, &data)
	}
	if data.Devices == nil {
		data.Devices = make(map[string]alexa.AlexaDeviceProxy)
	}
	data.Devices[id] = proxy

	newData, _ := json.Marshal(data)
	p.storage.Data = newData

	return nil
}

// OnEvent handles events from the system.
func (p *PluginAdapter) OnEvent(evt types.Event, entity types.Entity) (types.Entity, error) {
	// This hook is now used to receive all system events
	// We check if the event source matches any of our target entities from RawStore.

	var alexaDeviceID string
	if len(p.storage.Data) > 0 {
		var data struct {
			Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
		}
		if err := json.Unmarshal(p.storage.Data, &data); err == nil && data.Devices != nil {
			for id, proxy := range data.Devices {
				if proxy.TargetPluginID == evt.PluginID && proxy.TargetDeviceID == evt.DeviceID && proxy.TargetEntityID == evt.EntityID {
					alexaDeviceID = id
					break
				}
			}
		}
	}

	if alexaDeviceID == "" {
		// Not an Alexa-mapped device
		return entity, nil
	}

	// Check relay connection
	if p.relay == nil {
		return entity, alexa.ErrRelayDisconnected
	}
	if !p.relay.IsConnected() {
		return entity, alexa.ErrOffline
	}

	var payload map[string]any
	if err := json.Unmarshal(evt.Payload, &payload); err != nil {
		return entity, fmt.Errorf("%w: failed to unmarshal event: %v", alexa.ErrInvalidCommand, err)
	}

	// Translate event payload to Alexa state and push
	alexaState := alexa.StateFromProps(payload)
	if alexaState != nil {
		msg := map[string]any{
			"type": "alexaEvent",
			"event": map[string]any{
				"header": map[string]any{
					"namespace":      "Alexa",
					"name":           "ChangeReport",
					"messageId":      fmt.Sprintf("%d", time.Now().UnixNano()),
					"payloadVersion": "3",
				},
				"endpoint": map[string]any{
					"endpointId": alexaDeviceID,
				},
				"payload": map[string]any{
					"change": map[string]any{
						"cause":      map[string]any{"type": "PHYSICAL_INTERACTION"},
						"properties": alexaState["properties"],
					},
				},
			},
		}
		if err := p.relay.WriteJSON(msg); err != nil {
			return entity, fmt.Errorf("%w: failed to send change report: %v", alexa.ErrOffline, err)
		}
	}

	return entity, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// setSyncStatus sets the sync_status field in entity.Data
func setSyncStatus(entity types.Entity, status types.SyncStatus) types.Entity {
	entity.Data.SyncStatus = status
	return entity
}
