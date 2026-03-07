package alexa

import (
	"testing"
)

func TestRedactConnectToken(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "token with query param",
			input:    "wss://relay.example.com?connectToken=abcdef1234567890",
			expected: "wss://relay.example.com?connectToken=abcd...7890",
		},
		{
			name:     "short token",
			input:    "wss://relay.example.com?connectToken=short",
			expected: "wss://relay.example.com?connectToken=%2A%2A%2A%2A", // URL encoded ****
		},
		{
			name:     "no token",
			input:    "wss://relay.example.com",
			expected: "wss://relay.example.com",
		},
		{
			name:     "invalid URL",
			input:    "://invalid-url",
			expected: "://invalid-url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactConnectToken(tt.input)
			if result != tt.expected {
				t.Errorf("RedactConnectToken() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected float64
		ok       bool
	}{
		{"float64", 3.14, 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", 42, 42, true},
		{"int64", int64(100), 100, true},
		{"uint", uint(50), 50, true},
		{"string", "not a number", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := ToFloat(tt.input)
			if ok != tt.ok {
				t.Errorf("ToFloat() ok = %v, want %v", ok, tt.ok)
			}
			if ok && result != tt.expected {
				t.Errorf("ToFloat() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestBoolToOnOff(t *testing.T) {
	if BoolToOnOff(true) != "ON" {
		t.Error("BoolToOnOff(true) should return 'ON'")
	}
	if BoolToOnOff(false) != "OFF" {
		t.Error("BoolToOnOff(false) should return 'OFF'")
	}
}

func TestClampFloat(t *testing.T) {
	tests := []struct {
		v, min, max, expected float64
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{5, 5, 5, 5},
	}

	for _, tt := range tests {
		result := ClampFloat(tt.v, tt.min, tt.max)
		if result != tt.expected {
			t.Errorf("ClampFloat(%v, %v, %v) = %v, want %v", tt.v, tt.min, tt.max, result, tt.expected)
		}
	}
}

func TestFirstNonNil(t *testing.T) {
	if result := FirstNonNil(nil, nil, "first"); result != "first" {
		t.Errorf("FirstNonNil(nil, nil, 'first') = %v, want 'first'", result)
	}
	if result := FirstNonNil("first", "second"); result != "first" {
		t.Errorf("FirstNonNil('first', 'second') = %v, want 'first'", result)
	}
	if result := FirstNonNil(nil, nil, nil); result != nil {
		t.Errorf("FirstNonNil(nil, nil, nil) = %v, want nil", result)
	}
}

func TestRGBToHSV(t *testing.T) {
	// Test red
	h, s, v := RGBToHSV(1, 0, 0)
	if h != 0 || s != 1 || v != 1 {
		t.Errorf("RGBToHSV(1,0,0) = (%v, %v, %v), want (0, 1, 1)", h, s, v)
	}

	// Test green
	h, s, v = RGBToHSV(0, 1, 0)
	if h != 120 || s != 1 || v != 1 {
		t.Errorf("RGBToHSV(0,1,0) = (%v, %v, %v), want (120, 1, 1)", h, s, v)
	}

	// Test blue
	h, s, v = RGBToHSV(0, 0, 1)
	if h != 240 || s != 1 || v != 1 {
		t.Errorf("RGBToHSV(0,0,1) = (%v, %v, %v), want (240, 1, 1)", h, s, v)
	}

	// Test gray (saturation = 0)
	h, s, v = RGBToHSV(0.5, 0.5, 0.5)
	if s != 0 || v != 0.5 {
		t.Errorf("RGBToHSV(0.5, 0.5, 0.5) saturation = %v, want 0", s)
	}
}

func TestHSVToRGB(t *testing.T) {
	// Test red
	r, g, b := HSVToRGB(0, 1, 1)
	if r != 255 || g != 0 || b != 0 {
		t.Errorf("HSVToRGB(0,1,1) = (%v, %v, %v), want (255, 0, 0)", r, g, b)
	}

	// Test green
	r, g, b = HSVToRGB(120, 1, 1)
	if r != 0 || g != 255 || b != 0 {
		t.Errorf("HSVToRGB(120,1,1) = (%v, %v, %v), want (0, 255, 0)", r, g, b)
	}

	// Test blue
	r, g, b = HSVToRGB(240, 1, 1)
	if r != 0 || g != 0 || b != 255 {
		t.Errorf("HSVToRGB(240,1,1) = (%v, %v, %v), want (0, 0, 255)", r, g, b)
	}
}

func TestEventFactory_CreateDeleteReport(t *testing.T) {
	f := NewEventFactory()
	report := f.CreateDeleteReport("test-endpoint")

	event, ok := report["event"].(map[string]any)
	if !ok {
		t.Fatal("expected event to be map[string]any")
	}

	header, ok := event["header"].(map[string]any)
	if !ok {
		t.Fatal("expected header to be map[string]any")
	}

	if header["namespace"] != "Alexa.Discovery" {
		t.Errorf("namespace = %v, want Alexa.Discovery", header["namespace"])
	}
	if header["name"] != "DeleteReport" {
		t.Errorf("name = %v, want DeleteReport", header["name"])
	}

	payload, ok := event["payload"].(map[string]any)
	if !ok {
		t.Fatal("expected payload to be map[string]any")
	}

	endpoints, ok := payload["endpoints"].([]map[string]any)
	if !ok {
		t.Fatal("expected endpoints to be []map[string]any")
	}

	if len(endpoints) != 1 || endpoints[0]["endpointId"] != "test-endpoint" {
		t.Error("endpoint ID mismatch in DeleteReport")
	}
}

func TestEventFactory_CreateAddOrUpdateReport(t *testing.T) {
	f := NewEventFactory()
	endpoint := map[string]any{"endpointId": "test-device", "friendlyName": "Test"}
	report := f.CreateAddOrUpdateReport(endpoint)

	event, ok := report["event"].(map[string]any)
	if !ok {
		t.Fatal("expected event to be map[string]any")
	}

	header, ok := event["header"].(map[string]any)
	if !ok {
		t.Fatal("expected header to be map[string]any")
	}

	if header["name"] != "AddOrUpdateReport" {
		t.Errorf("name = %v, want AddOrUpdateReport", header["name"])
	}

	payload, ok := event["payload"].(map[string]any)
	if !ok {
		t.Fatal("expected payload to be map[string]any")
	}

	endpoints, ok := payload["endpoints"].([]map[string]any)
	if !ok {
		t.Fatal("expected endpoints to be []map[string]any")
	}

	if len(endpoints) != 1 || endpoints[0]["endpointId"] != "test-device" {
		t.Error("endpoint mismatch in AddOrUpdateReport")
	}
}

func TestStateFromProps(t *testing.T) {
	tests := []struct {
		name     string
		state    map[string]interface{}
		hasProps bool
	}{
		{
			name:     "power only",
			state:    map[string]interface{}{"power": true},
			hasProps: true,
		},
		{
			name:     "brightness",
			state:    map[string]interface{}{"brightness": 75},
			hasProps: true,
		},
		{
			name:     "color temperature",
			state:    map[string]interface{}{"temperature": 3000},
			hasProps: true,
		},
		{
			name:     "RGB color",
			state:    map[string]interface{}{"r": 255, "g": 128, "b": 64},
			hasProps: true,
		},
		{
			name:     "empty",
			state:    map[string]interface{}{},
			hasProps: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StateFromProps(tt.state)
			if tt.hasProps && result == nil {
				t.Error("expected properties but got nil")
			}
			if !tt.hasProps && result != nil {
				t.Errorf("expected nil but got %v", result)
			}
		})
	}
}

func TestStateFromPrimitive(t *testing.T) {
	tests := []struct {
		name     string
		vals     map[string]any
		hasProps bool
	}{
		{
			name:     "powerState",
			vals:     map[string]any{"powerState": "ON"},
			hasProps: true,
		},
		{
			name:     "brightness",
			vals:     map[string]any{"brightness": 50},
			hasProps: true,
		},
		{
			name:     "color temperature",
			vals:     map[string]any{"colorTemperatureInKelvin": 4000},
			hasProps: true,
		},
		{
			name:     "color",
			vals:     map[string]any{"color": map[string]any{"hue": 120, "saturation": 1, "brightness": 1}},
			hasProps: true,
		},
		{
			name:     "empty",
			vals:     map[string]any{},
			hasProps: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StateFromPrimitive(tt.vals)
			if tt.hasProps && result == nil {
				t.Error("expected properties but got nil")
			}
			if !tt.hasProps && result != nil {
				t.Errorf("expected nil but got %v", result)
			}
		})
	}
}
