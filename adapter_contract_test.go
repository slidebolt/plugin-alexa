package main

import (
	"encoding/json"
	"testing"

	"github.com/slidebolt/plugin-alexa/pkg/alexa"
	"github.com/slidebolt/sdk-types"
)

func TestOnDevicesList_AllIDsRemainStrings(t *testing.T) {
	p := &PluginAdapter{}

	storageData, err := json.Marshal(map[string]any{
		"devices": map[string]alexa.AlexaDeviceProxy{
			"proxy-1": {
				TargetPluginID: "target",
				TargetDeviceID: "device-1",
				TargetEntityID: "entity-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal storage: %v", err)
	}
	p.storage = types.Storage{Data: storageData}

	devices, err := p.OnDevicesList(nil)
	if err != nil {
		t.Fatalf("OnDevicesList failed: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("expected at least one device")
	}
	for _, d := range devices {
		if d.ID == "" {
			t.Fatalf("device ID must be non-empty: %+v", d)
		}
	}
}

func TestOnEntitiesList_PreservesCapabilityEntitiesAndAvailability(t *testing.T) {
	p := &PluginAdapter{}

	storageData, err := json.Marshal(map[string]any{
		"devices": map[string]alexa.AlexaDeviceProxy{
			"proxy-1": {
				TargetPluginID: "target",
				TargetDeviceID: "device-1",
				TargetEntityID: "entity-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal storage: %v", err)
	}
	p.storage = types.Storage{Data: storageData}

	current := []types.Entity{
		{ID: "temp-1", DeviceID: "proxy-1", Domain: "sensor", Actions: []string{"read_temperature"}},
		{ID: "lock-1", DeviceID: "proxy-1", Domain: "lock", Actions: []string{"lock", "unlock"}},
		{ID: "cover-1", DeviceID: "proxy-1", Domain: "cover", Actions: []string{"open", "close", "set_position"}},
	}

	entities, err := p.OnEntitiesList("proxy-1", current)
	if err != nil {
		t.Fatalf("OnEntitiesList failed: %v", err)
	}

	byID := map[string]types.Entity{}
	for _, e := range entities {
		byID[e.ID] = e
	}

	for _, id := range []string{"temp-1", "lock-1", "cover-1"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected entity %q to be preserved", id)
		}
	}
	if _, ok := byID["availability"]; !ok {
		t.Fatal("expected availability entity to be present for proxy device")
	}
}
