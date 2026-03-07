package alexa

import (
	"testing"

	"github.com/slidebolt/sdk-types"
)

func TestBuildEndpoint_SensorCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		domain       string
		actions      []string
		wantCaps     []string
		wantCategory string
	}{
		{
			name:         "temperature sensor",
			domain:       "sensor",
			actions:      []string{"read_temperature"},
			wantCaps:     []string{"Alexa.TemperatureSensor"},
			wantCategory: "THERMOSTAT",
		},
		{
			name:         "humidity sensor",
			domain:       "sensor",
			actions:      []string{"read_humidity"},
			wantCaps:     []string{"Alexa.RelativeHumiditySensor"},
			wantCategory: "THERMOSTAT",
		},
		{
			name:         "combined sensor",
			domain:       "climate",
			actions:      []string{"read_temperature", "read_humidity", "turn_on"},
			wantCaps:     []string{"Alexa.TemperatureSensor", "Alexa.RelativeHumiditySensor"},
			wantCategory: "THERMOSTAT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := types.Device{
				ID:         "test-sensor",
				SourceID:   "test-sensor",
				SourceName: "Test Sensor",
			}
			entities := []types.Entity{
				{
					ID:      "entity-1",
					Domain:  tt.domain,
					Actions: tt.actions,
				},
			}

			endpoint := BuildEndpoint(dev, entities)
			if endpoint == nil {
				t.Fatal("BuildEndpoint returned nil")
			}

			// Check display category
			categories := endpoint["displayCategories"].([]string)
			found := false
			for _, cat := range categories {
				if cat == tt.wantCategory {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("display category = %v, want %v", categories, tt.wantCategory)
			}

			// Check capabilities
			caps := endpoint["capabilities"].([]map[string]any)
			capMap := make(map[string]bool)
			for _, cap := range caps {
				if iface, ok := cap["interface"].(string); ok {
					capMap[iface] = true
				}
			}

			for _, wantCap := range tt.wantCaps {
				if !capMap[wantCap] {
					t.Errorf("missing capability: %s", wantCap)
				}
			}

			// Verify pure sensors (not climate) don't have PowerController
			if tt.domain == "sensor" && capMap["Alexa.PowerController"] {
				t.Error("pure sensors should not have PowerController")
			}
		})
	}
}

func TestBuildEndpoint_SecurityCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		domain       string
		actions      []string
		wantCaps     []string
		wantCategory string
	}{
		{
			name:         "lock",
			domain:       "lock",
			actions:      []string{"lock", "unlock"},
			wantCaps:     []string{"Alexa.LockController"},
			wantCategory: "SMARTLOCK",
		},
		{
			name:         "cover with position",
			domain:       "cover",
			actions:      []string{"open_cover", "close_cover", "set_cover_position"},
			wantCaps:     []string{"Alexa.ModeController"},
			wantCategory: "DOOR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dev := types.Device{
				ID:         "test-security",
				SourceID:   "test-security",
				SourceName: "Test Security",
			}
			entities := []types.Entity{
				{
					ID:      "entity-1",
					Domain:  tt.domain,
					Actions: tt.actions,
				},
			}

			endpoint := BuildEndpoint(dev, entities)
			if endpoint == nil {
				t.Fatal("BuildEndpoint returned nil")
			}

			// Check display category
			categories := endpoint["displayCategories"].([]string)
			found := false
			for _, cat := range categories {
				if cat == tt.wantCategory {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("display category = %v, want %v", categories, tt.wantCategory)
			}

			// Check capabilities
			caps := endpoint["capabilities"].([]map[string]any)
			capMap := make(map[string]bool)
			for _, cap := range caps {
				if iface, ok := cap["interface"].(string); ok {
					capMap[iface] = true
				}
			}

			for _, wantCap := range tt.wantCaps {
				if !capMap[wantCap] {
					t.Errorf("missing capability: %s", wantCap)
				}
			}
		})
	}
}

func TestStateFromProps_Sensors(t *testing.T) {
	tests := []struct {
		name     string
		state    map[string]interface{}
		wantNS   string
		wantName string
		wantVal  any
	}{
		{
			name:     "temperature in celsius",
			state:    map[string]interface{}{"temperature_c": 22.5},
			wantNS:   "Alexa.TemperatureSensor",
			wantName: "temperature",
			wantVal:  22.5,
		},
		{
			name:     "humidity",
			state:    map[string]interface{}{"humidity": 45},
			wantNS:   "Alexa.RelativeHumiditySensor",
			wantName: "relativeHumidity",
			wantVal:  45,
		},
		{
			name:     "lock locked",
			state:    map[string]interface{}{"locked": true},
			wantNS:   "Alexa.LockController",
			wantName: "lockState",
			wantVal:  "LOCKED",
		},
		{
			name:     "lock unlocked",
			state:    map[string]interface{}{"locked": false},
			wantNS:   "Alexa.LockController",
			wantName: "lockState",
			wantVal:  "UNLOCKED",
		},
		{
			name:     "cover open",
			state:    map[string]interface{}{"cover_position": 100},
			wantNS:   "Alexa.ModeController",
			wantName: "mode",
			wantVal:  "Position.Open",
		},
		{
			name:     "cover closed",
			state:    map[string]interface{}{"cover_position": 0},
			wantNS:   "Alexa.ModeController",
			wantName: "mode",
			wantVal:  "Position.Closed",
		},
		{
			name:     "cover partial",
			state:    map[string]interface{}{"cover_position": 50},
			wantNS:   "Alexa.ModeController",
			wantName: "mode",
			wantVal:  "Position.Partial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StateFromProps(tt.state)
			if result == nil {
				t.Fatal("StateFromProps returned nil")
			}

			props := result["properties"].([]map[string]any)
			found := false
			for _, prop := range props {
				if prop["namespace"] == tt.wantNS && prop["name"] == tt.wantName {
					found = true
					var val any
					if tt.wantNS == "Alexa.TemperatureSensor" {
						val = prop["value"].(map[string]any)["value"]
					} else {
						val = prop["value"]
					}
					// Allow for float64 vs int comparison
					if val != tt.wantVal {
						if vFloat, ok := val.(float64); ok {
							if wantFloat, ok := tt.wantVal.(float64); ok && vFloat == wantFloat {
								return
							}
						}
						t.Errorf("value = %v (%T), want %v (%T)", val, val, tt.wantVal, tt.wantVal)
					}
					return
				}
			}
			if !found {
				t.Errorf("property not found: namespace=%s, name=%s", tt.wantNS, tt.wantName)
			}
		})
	}
}

func TestProactiveReportingEnabled(t *testing.T) {
	// Verify that all capabilities have proactivelyReported: true
	dev := types.Device{
		ID:         "test-device",
		SourceID:   "test-device",
		SourceName: "Test Device",
	}
	entities := []types.Entity{
		{
			ID:      "entity-1",
			Domain:  "light",
			Actions: []string{"turn_on", "turn_off", "set_brightness", "set_rgb", "set_temperature", "read_temperature", "read_humidity", "lock", "unlock", "open_cover", "close_cover"},
		},
	}

	endpoint := BuildEndpoint(dev, entities)
	if endpoint == nil {
		t.Fatal("BuildEndpoint returned nil")
	}

	caps := endpoint["capabilities"].([]map[string]any)
	for _, cap := range caps {
		if iface, ok := cap["interface"].(string); ok && iface != "Alexa" {
			if props, ok := cap["properties"].(map[string]any); ok {
				if proactively, ok := props["proactivelyReported"].(bool); !ok || !proactively {
					t.Errorf("capability %s missing or has proactivelyReported=false", iface)
				}
			}
		}
	}
}
