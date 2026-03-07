package main

import (
	"encoding/json"
	"testing"

	"github.com/slidebolt/plugin-alexa/pkg/alexa"
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
	p := &PluginAdapter{}
	_, _ = p.OnInitialize(runner.Config{}, types.Storage{})

	proxyID := "test-alexa-1"
	payload := map[string]any{
		"type":             "add_device",
		"id":               proxyID,
		"target_plugin_id": "test-plugin",
		"target_device_id": "test-device",
		"target_entity_id": "test-entity",
	}
	raw, _ := json.Marshal(payload)
	// 1. Send add_device command to control entity
	_, err := p.OnCommand(types.Command{
		ID:      "cmd-1",
		Payload: json.RawMessage(raw),
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

	// 3. Validate persistence via OnStorageUpdate (simulating SDK behavior)
	// The plugin should marshal its state into the provided storage
	currentStorage := types.Storage{}
	storage, _ := p.OnStorageUpdate(currentStorage)
	var data struct {
		Devices map[string]alexa.AlexaDeviceProxy `json:"devices"`
	}
	json.Unmarshal(storage.Data, &data)
	if _, ok := data.Devices[proxyID]; !ok {
		t.Error("alexa device not persisted in storage data")
	}
}

func TestAlexaCommunication(t *testing.T) {
	sink := &mockEventSink{}

	// Setup storage with Alexa proxy mapping in RawStore
	proxyID := "alexa-device-1"
	targetDeviceID := "target-device"
	targetEntityID := "target-entity"

	storageData, _ := json.Marshal(map[string]any{
		"devices": map[string]alexa.AlexaDeviceProxy{
			proxyID: {
				TargetPluginID: "target-plugin",
				TargetDeviceID: targetDeviceID,
				TargetEntityID: targetEntityID,
			},
		},
	})

	p := &PluginAdapter{
		config: runner.Config{EventSink: sink},
	}
	p.storage = types.Storage{Data: storageData}

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

func TestOnDevicesList_DoesNotDuplicateControlDevice(t *testing.T) {
	p := &PluginAdapter{}
	_, _ = p.OnInitialize(runner.Config{}, types.Storage{})

	// Simulate persisted state where control already exists.
	current := []types.Device{
		{
			ID:         "control",
			SourceID:   "alexa-control",
			SourceName: "Alexa Control",
		},
	}

	devices, err := p.OnDevicesList(current)
	if err != nil {
		t.Fatalf("OnDevicesList failed: %v", err)
	}

	controlCount := 0
	for _, d := range devices {
		if d.ID == "control" {
			controlCount++
		}
	}
	if controlCount != 1 {
		t.Fatalf("expected exactly one control device, got %d (devices=%v)", controlCount, devices)
	}
}

func TestAlexaErrorStatusMapping(t *testing.T) {
	p := &PluginAdapter{}
	_, _ = p.OnInitialize(runner.Config{}, types.Storage{})

	tests := []struct {
		name           string
		payload        map[string]any
		wantSyncStatus types.SyncStatus
		wantErr        bool
	}{
		{
			name: "successful command sets synced",
			payload: map[string]any{
				"type": "add_device",
				"id":   "test-device-1",
			},
			wantSyncStatus: types.SyncStatusSynced,
			wantErr:        false,
		},
		{
			name: "invalid json sets failed",
			payload: map[string]any{
				"type": "add_device",
				"id":   "test-device-2",
			},
			wantSyncStatus: types.SyncStatusFailed,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw []byte
			var err error

			if tt.wantErr && tt.name == "invalid json sets failed" {
				// Create invalid JSON
				raw = []byte(`{invalid json}`)
			} else {
				raw, _ = json.Marshal(tt.payload)
			}

			entity, err := p.OnCommand(types.Command{
				ID:      "cmd-test",
				Payload: json.RawMessage(raw),
			}, types.Entity{ID: "control"})

			if tt.wantErr && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if entity.Data.SyncStatus != tt.wantSyncStatus {
				t.Errorf("sync_status = %v, want %v", entity.Data.SyncStatus, tt.wantSyncStatus)
			}
		})
	}
}

func TestSyncStatusStandards(t *testing.T) {
	// Verify the correct standard values are used
	if alexa.SyncStatusSynced != "synced" {
		t.Errorf("SyncStatusSynced = %v, want 'synced'", alexa.SyncStatusSynced)
	}
	if alexa.SyncStatusPending != "pending" {
		t.Errorf("SyncStatusPending = %v, want 'pending'", alexa.SyncStatusPending)
	}
	if alexa.SyncStatusFailed != "failed" {
		t.Errorf("SyncStatusFailed = %v, want 'failed'", alexa.SyncStatusFailed)
	}
	if alexa.SyncStatusEmpty != "" {
		t.Errorf("SyncStatusEmpty = %v, want ''", alexa.SyncStatusEmpty)
	}

	// Verify we don't use non-standard values
	invalidStatuses := []string{"in_sync", "ok", "error"}
	for _, status := range invalidStatuses {
		if status == alexa.SyncStatusSynced ||
			status == alexa.SyncStatusPending ||
			status == alexa.SyncStatusFailed {
			t.Errorf("non-standard status '%s' should not be a constant", status)
		}
	}
}
