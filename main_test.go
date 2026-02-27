package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/slidebolt/sdk-types"
)

func TestAlexaDirectiveForwarding(t *testing.T) {
	p := &PluginAlexaPlugin{
		alexaDevices: make(map[string]AlexaDeviceProxy),
	}
	p.factory = NewAlexaEventFactory()
	
	p.alexaDevices["alexa-device-1"] = AlexaDeviceProxy{
		TargetPluginID: "target-plugin",
		TargetDeviceID: "target-device",
		TargetEntityID: "target-entity",
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Skip("NATS not available")
	}
	defer nc.Close()
	// p.nc removed as it is no longer a field in PluginAlexaPlugin

	sub, _ := nc.SubscribeSync("slidebolt.rpc.target-plugin")

	payload := map[string]any{
		"type": "alexaDirective",
		"directive": map[string]any{
			"header": map[string]any{
				"namespace": "Alexa.PowerController",
				"name":      "TurnOn",
			},
			"endpoint": map[string]any{
				"endpointId": "alexa-device-1",
			},
			"payload": map[string]any{},
		},
	}

	p.handleRelayMessage(payload)

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("did not receive NATS message: %v", err)
	}

	var req types.Request
	json.Unmarshal(msg.Data, &req)
	if req.Method != "entities/commands/create" {
		t.Errorf("unexpected method: %s", req.Method)
	}

	var params struct {
		DeviceID string          `json:"device_id"`
		EntityID string          `json:"entity_id"`
		Payload  json.RawMessage `json:"payload"`
	}
	json.Unmarshal(req.Params, &params)
	if params.DeviceID != "target-device" || params.EntityID != "target-entity" {
		t.Errorf("unexpected target: %v", params)
	}

	var cmdPayload map[string]any
	json.Unmarshal(params.Payload, &cmdPayload)
	if cmdPayload["type"] != "TurnOn" {
		t.Errorf("unexpected command payload: %v", cmdPayload)
	}
}