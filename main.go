package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"time"

	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
	"github.com/slidebolt/sdk-entities/light"
)

type PluginAlexaPlugin struct {
	config  runner.Config
	storage types.Storage
	mu      sync.RWMutex

	relay   *RelayClient
	factory *AlexaEventFactory
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Mapping of DeviceID -> Alexa proxy metadata
	alexaDevices map[string]AlexaDeviceProxy
}

type AlexaDeviceProxy struct {
	TargetPluginID string `json:"target_plugin_id"`
	TargetDeviceID string `json:"target_device_id"`
	TargetEntityID string `json:"target_entity_id"`
}

type AlexaPluginConfig struct {
	WSEndpoint   string `json:"ws_endpoint"`
	ClientSecret string `json:"client_secret"`
	ClientID     string `json:"client_id"`
	RelayToken   string `json:"relay_token"`
}

func (p *PluginAlexaPlugin) OnInitialize(config runner.Config, state types.Storage) (types.Manifest, types.Storage) {
	p.config = config
	p.storage = state
	p.factory = NewAlexaEventFactory()
	p.alexaDevices = make(map[string]AlexaDeviceProxy)

	if len(state.Data) > 0 {
		var data struct {
			Devices map[string]AlexaDeviceProxy `json:"devices"`
		}
		if err := json.Unmarshal(state.Data, &data); err == nil && data.Devices != nil {
			p.alexaDevices = data.Devices
		}
	}

	return types.Manifest{
		ID:      "plugin-alexa",
		Name:    "Alexa Bridge",
		Version: "1.1.0",
	}, state
}

func (p *PluginAlexaPlugin) OnReady() {
	p.ctx, p.cancel = context.WithCancel(context.Background())

	cfg := p.resolveConfig()
	if cfg.WSEndpoint == "" || cfg.ClientSecret == "" {
		fmt.Println("[alexa] missing relay configuration, bridge inactive")
		return
	}

	p.relay = NewRelayClient(
		cfg.WSEndpoint, cfg.ClientSecret, cfg.ClientID,
		DefaultLogger{},
		p.handleRelayMessage,
		p.onRelayConnected,
	)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.relay.RunLoop(p.ctx)
	}()
}

func (p *PluginAlexaPlugin) resolveConfig() AlexaPluginConfig {
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

func (p *PluginAlexaPlugin) onRelayConnected() {
	fmt.Println("[alexa] relay connected")
}

func (p *PluginAlexaPlugin) handleRelayMessage(payload map[string]any) {
	if msgType, _ := payload["type"].(string); msgType == "alexaDirective" {
		p.handleDirective(payload)
	}
}

func (p *PluginAlexaPlugin) handleDirective(payload map[string]any) {
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
		return
	}

	deviceID := envelope.Directive.Endpoint.EndpointID
	p.mu.RLock()
	proxy, ok := p.alexaDevices[deviceID]
	p.mu.RUnlock()

	if !ok {
		fmt.Printf("[alexa] directive for unknown device: %s\n", deviceID)
		return
	}

	p.forwardDirectiveToTarget(proxy, envelope.Directive.Header.Namespace, envelope.Directive.Header.Name, envelope.Directive.Payload)
}

func (p *PluginAlexaPlugin) forwardDirectiveToTarget(proxy AlexaDeviceProxy, namespace, name string, payload map[string]any) {
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
				h, _ := toFloat(color["hue"])
				s, _ := toFloat(color["saturation"])
				v, _ := toFloat(color["brightness"])
				r, g, b := hsvToRGB(h, s, v)
				cmdPayload["type"] = light.ActionSetRGB
				cmdPayload["rgb"] = []int{r, g, b}
			}
		}
	case "Alexa.ColorTemperatureController":
		if v, ok := payload["colorTemperatureInKelvin"]; ok {
			cmdPayload["type"] = light.ActionSetTemperature
			cmdPayload["temperature"] = v
		}
	}

	// We use the SDK's EventSink to emit an event representing the directive.
	// This event can be observed by other plugins or used to trigger automations.
	body, _ := json.Marshal(cmdPayload)
	err := p.config.EventSink.EmitEvent(types.InboundEvent{
		DeviceID: proxy.TargetDeviceID,
		EntityID: proxy.TargetEntityID,
		Payload:  body,
	})
	if err != nil {
		fmt.Printf("[alexa] failed to emit directive event: %v\n", err)
	}
}

func (p *PluginAlexaPlugin) OnHealthCheck() (string, error) {
	return "perfect", nil
}

func (p *PluginAlexaPlugin) OnStorageUpdate(current types.Storage) (types.Storage, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	data, _ := json.Marshal(map[string]any{"devices": p.alexaDevices})
	p.storage.Data = data
	return p.storage, nil
}

func (p *PluginAlexaPlugin) OnDeviceCreate(dev types.Device) (types.Device, error) { return dev, nil }
func (p *PluginAlexaPlugin) OnDeviceUpdate(dev types.Device) (types.Device, error) { return dev, nil }
func (p *PluginAlexaPlugin) OnDeviceDelete(id string) error                        { return nil }
func (p *PluginAlexaPlugin) OnDevicesList(current []types.Device) ([]types.Device, error) {
	// Always include the control device
	control := types.Device{
		ID:         "control",
		SourceID:   "alexa-control",
		SourceName: "Alexa Control",
	}
	return append(current, control), nil
}
func (p *PluginAlexaPlugin) OnDeviceSearch(q types.SearchQuery, res []types.Device) ([]types.Device, error) {
	return res, nil
}

func (p *PluginAlexaPlugin) OnEntityCreate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginAlexaPlugin) OnEntityUpdate(e types.Entity) (types.Entity, error) { return e, nil }
func (p *PluginAlexaPlugin) OnEntityDelete(d, e string) error                    { return nil }
func (p *PluginAlexaPlugin) OnEntitiesList(d string, c []types.Entity) ([]types.Entity, error) {
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
	return c, nil
}

func (p *PluginAlexaPlugin) OnCommand(cmd types.Command, entity types.Entity) (types.Entity, error) {
	if entity.ID == "control" {
		var payload map[string]any
		json.Unmarshal(cmd.Payload, &payload)
		action, _ := payload["type"].(string)
		if action == "add_device" {
			p.handleAddDevice(payload)
		}
	}
	return entity, nil
}

func (p *PluginAlexaPlugin) handleAddDevice(payload map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	
	id, _ := payload["id"].(string)
	if id == "" { return }
	
	proxy := AlexaDeviceProxy{
		TargetPluginID: asString(payload["target_plugin_id"]),
		TargetDeviceID: asString(payload["target_device_id"]),
		TargetEntityID: asString(payload["target_entity_id"]),
	}
	p.alexaDevices[id] = proxy
}

func (p *PluginAlexaPlugin) OnEvent(evt types.Event, entity types.Entity) (types.Entity, error) {
	// This hook is now used to receive all system events (as per TASK.md)
	// We check if the event source matches any of our target entities.
	
	p.mu.RLock()
	var alexaDeviceID string
	for id, proxy := range p.alexaDevices {
		if proxy.TargetPluginID == evt.PluginID && proxy.TargetDeviceID == evt.DeviceID && proxy.TargetEntityID == evt.EntityID {
			alexaDeviceID = id
			break
		}
	}
	p.mu.RUnlock()

	if alexaDeviceID != "" && p.relay != nil && p.relay.IsConnected() {
		// Translate event payload to Alexa state and push
		var payload map[string]any
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			alexaState := stateFromProps(payload)
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
								"cause": map[string]any{"type": "PHYSICAL_INTERACTION"},
								"properties": alexaState["properties"],
							},
						},
					},
				}
				p.relay.WriteJSON(msg)
			}
		}
	}
	
	return entity, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func main() {
	plugin := &PluginAlexaPlugin{}
	if err := runner.NewRunner(plugin).Run(); err != nil {
		log.Fatal(err)
	}
}