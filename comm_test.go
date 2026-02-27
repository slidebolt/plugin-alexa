package main

import (
	"encoding/json"
	"testing"

	"github.com/slidebolt/sdk-entities/light"
	"github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

// mockEventSink captures emitted events for verification
type mockEventSink struct {
	events []types.InboundEvent
}

func (m *mockEventSink) EmitEvent(evt types.InboundEvent) error {
	m.events = append(m.events, evt)
	return nil
}

func TestCreateAlexaDevice(t *testing.T) {
	p := &PluginAlexaPlugin{}
	_, _ = p.OnInitialize(runner.Config{}, types.Storage{})

	proxyID := "test-alexa-1"
	payload := map[string]any{
		"type":             "add_device",
		"id":               proxyID,
		"target_plugin_id": "test-plugin",
		"target_device_id": "test-device",
		"target_entity_id": "test-entity",
	}
	body, _ := json.Marshal(payload)

	// 1. Send add_device command to control entity
	_, err := p.OnCommand(types.Command{
		EntityID: "control",
		Payload:  body,
	}, types.Entity{ID: "control"})

	if err != nil {
		t.Fatalf("OnCommand failed: %v", err)
	}

	// 2. Validate device exists in list
	devices, _ := p.OnDevicesList([]types.Device{})
	found := false
	for _, d := range devices {
		if d.ID == proxyID {
			found = true
			break
		}
	}
	if !found {
		t.Error("created alexa device not found in OnDevicesList")
	}

	// 3. Validate persistence via OnStorageUpdate
	storage, _ := p.OnStorageUpdate(types.Storage{})
	var data struct {
		Devices map[string]AlexaDeviceProxy `json:"devices"`
	}
	json.Unmarshal(storage.Data, &data)
	if _, ok := data.Devices[proxyID]; !ok {
		t.Error("alexa device not persisted in storage data")
	}
}

func TestAlexaCommunication(t *testing.T) {
	sink := &mockEventSink{}
	p := &PluginAlexaPlugin{
		config:       runner.Config{EventSink: sink},
		alexaDevices: make(map[string]AlexaDeviceProxy),
	}
	
	proxyID := "alexa-device-1"
	targetDeviceID := "target-device"
	targetEntityID := "target-entity"
	
	p.alexaDevices[proxyID] = AlexaDeviceProxy{
		TargetPluginID: "target-plugin",
		TargetDeviceID: targetDeviceID,
		TargetEntityID: targetEntityID,
	}

	// Simulate an Alexa directive (TurnOn)
	directive := map[string]any{
		"type": "alexaDirective",
		"directive": map[string]any{
			"header": map[string]any{
				"namespace": "Alexa.PowerController",
				"name":      "TurnOn",
			},
			"endpoint": map[string]any{
				"endpointId": proxyID,
			},
			"payload": map[string]any{},
		},
	}

	p.handleRelayMessage(directive)

	// Verify that EventSink received the translated event
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}

	evt := sink.events[0]
	if evt.DeviceID != targetDeviceID || evt.EntityID != targetEntityID {
		t.Errorf("event target mismatch: got %s/%s, want %s/%s", evt.DeviceID, evt.EntityID, targetDeviceID, targetEntityID)
	}

	var payload map[string]any
	json.Unmarshal(evt.Payload, &payload)
	if payload["type"] != light.ActionTurnOn {
		t.Errorf("event payload mismatch: got %v, want %s", payload["type"], light.ActionTurnOn)
	}
}
