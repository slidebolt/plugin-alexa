package main

import (
	"encoding/json"
	"testing"

	"github.com/slidebolt/plugin-alexa/pkg/alexa"
	"github.com/slidebolt/sdk-entities/light"
	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

func TestAlexaDirectiveForwarding(t *testing.T) {
	sink := &mockEventSink{}

	// Setup storage with Alexa proxy mapping in RawStore
	storageData, _ := json.Marshal(map[string]any{
		"devices": map[string]alexa.AlexaDeviceProxy{
			"alexa-device-1": {
				TargetPluginID: "target-plugin",
				TargetDeviceID: "target-device",
				TargetEntityID: "target-entity",
			},
		},
	})

	p := &PluginAdapter{
		pluginCtx: runner.PluginContext{Events: alexaLegacyEventService{sink: sink}},
	}
	p.storage = types.Storage{Data: storageData}
	p.factory = alexa.NewEventFactory()

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

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	evt := sink.events[0]
	if evt.DeviceID != "target-device" || evt.EntityID != "target-entity" {
		t.Errorf("unexpected target: %s/%s", evt.DeviceID, evt.EntityID)
	}

	var resPayload map[string]any
	json.Unmarshal(evt.Payload, &resPayload)
	if resPayload["type"] != light.ActionTurnOn {
		t.Errorf("unexpected command payload: %v", resPayload)
	}
}
