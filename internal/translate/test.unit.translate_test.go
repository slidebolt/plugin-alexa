package translate

import (
	"encoding/json"
	"testing"

	domain "github.com/slidebolt/sb-domain"
)

// ---------------------------------------------------------------------------
// Decode tests
// ---------------------------------------------------------------------------

func TestDecode_Light(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantOK     bool
		wantPower  bool
		wantBright int
	}{
		{"basic on", `{"power":true,"brightness":200}`, true, true, 200},
		{"off zero", `{"power":false,"brightness":0}`, true, false, 0},
		{"clamp high", `{"power":true,"brightness":300}`, true, true, 254},
		{"clamp negative", `{"power":true,"brightness":-5}`, true, true, 0},
		{"empty", ``, false, false, 0},
		{"bad json", `{not json}`, false, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Decode("light", json.RawMessage(tc.raw))
			if ok != tc.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			s := got.(domain.Light)
			if s.Power != tc.wantPower {
				t.Errorf("Power: got %v, want %v", s.Power, tc.wantPower)
			}
			if s.Brightness != tc.wantBright {
				t.Errorf("Brightness: got %d, want %d", s.Brightness, tc.wantBright)
			}
		})
	}
}

func TestDecode_Switch(t *testing.T) {
	got, ok := Decode("switch", json.RawMessage(`{"power":true}`))
	if !ok {
		t.Fatal("expected ok")
	}
	s := got.(domain.Switch)
	if !s.Power {
		t.Error("expected power on")
	}
}

func TestDecode_Cover(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantOK   bool
		wantPos  int
	}{
		{"normal", `{"position":75}`, true, 75},
		{"clamp high", `{"position":150}`, true, 100},
		{"clamp low", `{"position":-10}`, true, 0},
		{"empty", ``, false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Decode("cover", json.RawMessage(tc.raw))
			if ok != tc.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			s := got.(domain.Cover)
			if s.Position != tc.wantPos {
				t.Errorf("Position: got %d, want %d", s.Position, tc.wantPos)
			}
		})
	}
}

func TestDecode_Lock(t *testing.T) {
	got, ok := Decode("lock", json.RawMessage(`{"locked":true}`))
	if !ok {
		t.Fatal("expected ok")
	}
	s := got.(domain.Lock)
	if !s.Locked {
		t.Error("expected locked")
	}
}

func TestDecode_Fan(t *testing.T) {
	got, ok := Decode("fan", json.RawMessage(`{"power":true,"percentage":75}`))
	if !ok {
		t.Fatal("expected ok")
	}
	s := got.(domain.Fan)
	if !s.Power || s.Percentage != 75 {
		t.Errorf("got %+v", s)
	}
}

func TestDecode_Sensor(t *testing.T) {
	got, ok := Decode("sensor", json.RawMessage(`{"value":22.5,"unit":"°C"}`))
	if !ok {
		t.Fatal("expected ok")
	}
	s := got.(domain.Sensor)
	if s.Value != 22.5 || s.Unit != "°C" {
		t.Errorf("got %+v", s)
	}
}

func TestDecode_BinarySensor(t *testing.T) {
	got, ok := Decode("binary_sensor", json.RawMessage(`{"on":true}`))
	if !ok {
		t.Fatal("expected ok")
	}
	s := got.(domain.BinarySensor)
	if !s.On {
		t.Error("expected on")
	}
}

func TestDecode_Climate(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantOK   bool
		wantMode string
		wantTemp float64
	}{
		{"cool mode", `{"hvacMode":"cool","temperature":21}`, true, "cool", 21},
		{"off", `{"hvacMode":"off","temperature":0}`, true, "off", 0},
		{"empty", ``, false, "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Decode("climate", json.RawMessage(tc.raw))
			if ok != tc.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			s := got.(domain.Climate)
			if s.HVACMode != tc.wantMode {
				t.Errorf("HVACMode: got %q, want %q", s.HVACMode, tc.wantMode)
			}
			if s.Temperature != tc.wantTemp {
				t.Errorf("Temperature: got %v, want %v", s.Temperature, tc.wantTemp)
			}
		})
	}
}

func TestDecode_UnknownType(t *testing.T) {
	_, ok := Decode("thermostat_v2", json.RawMessage(`{"foo":"bar"}`))
	if ok {
		t.Fatal("expected unknown entity type to return ok=false")
	}
}

func TestDecode_AllNewTypes(t *testing.T) {
	tests := []struct {
		typ string
		raw string
	}{
		{"alarm", `{"alarmState":"disarmed"}`},
		{"camera", `{"isRecording":false}`},
		{"valve", `{"position":50}`},
		{"siren", `{"isOn":true}`},
		{"humidifier", `{"isOn":true,"targetHumidity":60}`},
		{"media_player", `{"state":"playing"}`},
		{"remote", `{"isOn":true}`},
		{"event", `{"eventTypes":["motion"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.typ, func(t *testing.T) {
			_, ok := Decode(tc.typ, json.RawMessage(tc.raw))
			if !ok {
				t.Fatalf("expected ok for %s", tc.typ)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Encode tests
// ---------------------------------------------------------------------------

func TestEncode_LightTurnOn(t *testing.T) {
	raw, err := Encode(domain.LightTurnOn{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("expected non-empty payload")
	}
}

func TestEncode_BrightnessRange(t *testing.T) {
	_, err := Encode(domain.LightSetBrightness{Brightness: 300}, nil)
	if err == nil {
		t.Fatal("expected error for out-of-range brightness")
	}
}

// ---------------------------------------------------------------------------
// ToAlexa tests
// ---------------------------------------------------------------------------

func TestToAlexa_Light(t *testing.T) {
	entity := domain.Entity{
		ID: "light1", Plugin: "test", DeviceID: "dev1",
		Type: "light", Name: "Test Light",
		State: domain.Light{Power: true, Brightness: 200},
	}
	ep := ToAlexa(entity)
	if ep.EndpointID != entity.Key() {
		t.Errorf("EndpointID: got %q, want %q", ep.EndpointID, entity.Key())
	}
	if ep.FriendlyName != "Test Light" {
		t.Errorf("FriendlyName: got %q", ep.FriendlyName)
	}
	if len(ep.DisplayCategories) == 0 || ep.DisplayCategories[0] != "LIGHT" {
		t.Errorf("DisplayCategories: got %v", ep.DisplayCategories)
	}
	// Should have Alexa, PowerController, BrightnessController, EndpointHealth
	found := map[string]bool{}
	for _, c := range ep.Capabilities {
		found[c.Interface] = true
	}
	for _, want := range []string{"Alexa", "Alexa.PowerController", "Alexa.BrightnessController", "Alexa.EndpointHealth"} {
		if !found[want] {
			t.Errorf("missing capability %q", want)
		}
	}
}

func TestToAlexa_Switch(t *testing.T) {
	entity := domain.Entity{
		ID: "sw1", Plugin: "test", DeviceID: "dev1",
		Type: "switch", Name: "Test Switch",
		State: domain.Switch{Power: true},
	}
	ep := ToAlexa(entity)
	if ep.DisplayCategories[0] != "SWITCH" {
		t.Errorf("DisplayCategories: got %v", ep.DisplayCategories)
	}
}

func TestToAlexa_Lock(t *testing.T) {
	entity := domain.Entity{
		ID: "lock1", Plugin: "test", DeviceID: "dev1",
		Type: "lock", Name: "Front Door",
		State: domain.Lock{Locked: true},
	}
	ep := ToAlexa(entity)
	if ep.DisplayCategories[0] != "SMARTLOCK" {
		t.Errorf("DisplayCategories: got %v", ep.DisplayCategories)
	}
	found := false
	for _, c := range ep.Capabilities {
		if c.Interface == "Alexa.LockController" {
			found = true
		}
	}
	if !found {
		t.Error("missing Alexa.LockController capability")
	}
}

func TestToAlexa_Climate(t *testing.T) {
	entity := domain.Entity{
		ID: "hvac1", Plugin: "test", DeviceID: "dev1",
		Type: "climate", Name: "Thermostat",
		State: domain.Climate{HVACMode: "cool", Temperature: 22},
	}
	ep := ToAlexa(entity)
	if ep.DisplayCategories[0] != "THERMOSTAT" {
		t.Errorf("DisplayCategories: got %v", ep.DisplayCategories)
	}
}

// ---------------------------------------------------------------------------
// AlexaState tests
// ---------------------------------------------------------------------------

func TestAlexaState_Light(t *testing.T) {
	entity := domain.Entity{
		ID: "light1", Plugin: "test", DeviceID: "dev1",
		Type: "light", Name: "Test Light",
		State: domain.Light{Power: true, Brightness: 127},
	}
	props := AlexaState(entity)
	found := map[string]any{}
	for _, p := range props {
		found[p.Namespace+"/"+p.Name] = p.Value
	}
	if found["Alexa.PowerController/powerState"] != "ON" {
		t.Errorf("powerState: got %v", found["Alexa.PowerController/powerState"])
	}
	b, ok := found["Alexa.BrightnessController/brightness"].(int)
	if !ok || b != 50 {
		t.Errorf("brightness: got %v (expected 50)", found["Alexa.BrightnessController/brightness"])
	}
}

func TestAlexaState_Lock(t *testing.T) {
	entity := domain.Entity{
		ID: "lock1", Plugin: "test", DeviceID: "dev1",
		Type: "lock", Name: "Door",
		State: domain.Lock{Locked: true},
	}
	props := AlexaState(entity)
	for _, p := range props {
		if p.Namespace == "Alexa.LockController" && p.Name == "lockState" {
			if p.Value != "LOCKED" {
				t.Errorf("lockState: got %v", p.Value)
			}
			return
		}
	}
	t.Error("missing lockState property")
}

// ---------------------------------------------------------------------------
// FromAlexa tests
// ---------------------------------------------------------------------------

func TestFromAlexa_PowerController_TurnOn(t *testing.T) {
	tests := []struct {
		entityType string
	}{
		{"light"}, {"switch"}, {"fan"}, {"siren"}, {"humidifier"}, {"remote"},
	}
	for _, tc := range tests {
		t.Run(tc.entityType, func(t *testing.T) {
			cmd, err := FromAlexa(tc.entityType, "Alexa.PowerController", "TurnOn", nil)
			if err != nil {
				t.Fatal(err)
			}
			if cmd == nil {
				t.Fatal("expected non-nil command")
			}
		})
	}
}

func TestFromAlexa_PowerController_TurnOff(t *testing.T) {
	cmd, err := FromAlexa("light", "Alexa.PowerController", "TurnOff", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cmd.(domain.LightTurnOff); !ok {
		t.Fatalf("expected LightTurnOff, got %T", cmd)
	}
}

func TestFromAlexa_BrightnessController(t *testing.T) {
	cmd, err := FromAlexa("light", "Alexa.BrightnessController", "SetBrightness", map[string]any{
		"brightness": float64(50),
	})
	if err != nil {
		t.Fatal(err)
	}
	sb, ok := cmd.(domain.LightSetBrightness)
	if !ok {
		t.Fatalf("expected LightSetBrightness, got %T", cmd)
	}
	// 50% Alexa → 127 SB
	if sb.Brightness != 127 {
		t.Errorf("brightness: got %d, want 127", sb.Brightness)
	}
}

func TestFromAlexa_ColorController(t *testing.T) {
	cmd, err := FromAlexa("light", "Alexa.ColorController", "SetColor", map[string]any{
		"color": map[string]any{
			"hue":        float64(120),
			"saturation": float64(0.5),
			"brightness": float64(1.0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hs, ok := cmd.(domain.LightSetHS)
	if !ok {
		t.Fatalf("expected LightSetHS, got %T", cmd)
	}
	if hs.Hue != 120 {
		t.Errorf("hue: got %v, want 120", hs.Hue)
	}
	if hs.Saturation != 50 {
		t.Errorf("saturation: got %v, want 50", hs.Saturation)
	}
}

func TestFromAlexa_LockController(t *testing.T) {
	cmd, err := FromAlexa("lock", "Alexa.LockController", "Lock", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cmd.(domain.LockLock); !ok {
		t.Fatalf("expected LockLock, got %T", cmd)
	}
}

func TestFromAlexa_ThermostatController(t *testing.T) {
	cmd, err := FromAlexa("climate", "Alexa.ThermostatController", "SetTargetTemperature", map[string]any{
		"targetSetpoint": map[string]any{"value": float64(22), "scale": "CELSIUS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, ok := cmd.(domain.ClimateSetTemperature)
	if !ok {
		t.Fatalf("expected ClimateSetTemperature, got %T", cmd)
	}
	if st.Temperature != 22 {
		t.Errorf("temperature: got %v, want 22", st.Temperature)
	}
}

func TestFromAlexa_RangeController(t *testing.T) {
	tests := []struct {
		instance   string
		entityType string
		value      float64
	}{
		{"Cover.Position", "cover", 75},
		{"Fan.Speed", "fan", 50},
		{"Valve.Position", "valve", 30},
	}
	for _, tc := range tests {
		t.Run(tc.instance, func(t *testing.T) {
			cmd, err := FromAlexa(tc.entityType, "Alexa.RangeController", "SetRangeValue", map[string]any{
				"_instance":  tc.instance,
				"rangeValue": tc.value,
			})
			if err != nil {
				t.Fatal(err)
			}
			if cmd == nil {
				t.Fatal("expected non-nil command")
			}
		})
	}
}

func TestFromAlexa_PlaybackController(t *testing.T) {
	tests := []struct {
		name    string
		wantTyp string
	}{
		{"Play", "MediaPlay"},
		{"Pause", "MediaPause"},
		{"Stop", "MediaStop"},
		{"Next", "MediaNextTrack"},
		{"Previous", "MediaPreviousTrack"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := FromAlexa("media_player", "Alexa.PlaybackController", tc.name, nil)
			if err != nil {
				t.Fatal(err)
			}
			if cmd == nil {
				t.Fatal("expected non-nil command")
			}
		})
	}
}

func TestFromAlexa_SecurityPanelController(t *testing.T) {
	cmd, err := FromAlexa("alarm", "Alexa.SecurityPanelController", "Arm", map[string]any{
		"armState": "ARMED_AWAY",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cmd.(domain.AlarmArmAway); !ok {
		t.Fatalf("expected AlarmArmAway, got %T", cmd)
	}
}

func TestFromAlexa_Unsupported(t *testing.T) {
	_, err := FromAlexa("light", "Alexa.Bogus", "DoStuff", nil)
	if err == nil {
		t.Fatal("expected error for unsupported directive")
	}
}

// ---------------------------------------------------------------------------
// Brightness scaling tests
// ---------------------------------------------------------------------------

func TestBrightnessScaling(t *testing.T) {
	tests := []struct {
		sb    int
		alexa int
	}{
		{0, 0},
		{127, 50},
		{254, 100},
	}
	for _, tc := range tests {
		a := sbBrightnessToAlexa(tc.sb)
		if a != tc.alexa {
			t.Errorf("sbToAlexa(%d): got %d, want %d", tc.sb, a, tc.alexa)
		}
		s := alexaBrightnessToSB(tc.alexa)
		if s != tc.sb {
			t.Errorf("alexaToSB(%d): got %d, want %d", tc.alexa, s, tc.sb)
		}
	}
}

// ---------------------------------------------------------------------------
// Color conversion tests
// ---------------------------------------------------------------------------

func TestRGBtoHSV_Red(t *testing.T) {
	h, s, v := rgbToHSV(255, 0, 0)
	if h != 0 || s != 1 || v != 1 {
		t.Errorf("red: got h=%v s=%v v=%v", h, s, v)
	}
}

func TestHSVtoRGB_Red(t *testing.T) {
	r, g, b := hsvToRGB(0, 1, 1)
	if r != 255 || g != 0 || b != 0 {
		t.Errorf("red: got r=%d g=%d b=%d", r, g, b)
	}
}

func TestRGBtoHSV_Green(t *testing.T) {
	h, s, v := rgbToHSV(0, 255, 0)
	if h != 120 || s != 1 || v != 1 {
		t.Errorf("green: got h=%v s=%v v=%v", h, s, v)
	}
}

func TestRGBtoHSV_Black(t *testing.T) {
	h, s, v := rgbToHSV(0, 0, 0)
	if h != 0 || s != 0 || v != 0 {
		t.Errorf("black: got h=%v s=%v v=%v", h, s, v)
	}
}
