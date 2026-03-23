package translate

// translate.go — Alexa Smart Home Skill translation layer.
//
// This file translates between SlideBolt domain types and the Alexa Smart Home
// Skill API v3 format. The plugin builds fully-formed Alexa payloads; the relay
// is a dumb forwarder.
//
//   Decode: raw protocol bytes → canonical domain state (lenient, identity codec)
//   Encode: canonical domain command → raw protocol bytes (strict, identity codec)
//   ToAlexa: domain.Entity → AlexaEndpoint (discovery) + []AlexaProperty (state)
//   FromAlexa: Alexa directive → SlideBolt domain command

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	domain "github.com/slidebolt/sb-domain"
)

// ---------------------------------------------------------------------------
// Alexa types
// ---------------------------------------------------------------------------

// AlexaEndpoint represents a device in the Alexa discovery response.
type AlexaEndpoint struct {
	EndpointID        string            `json:"endpointId"`
	FriendlyName      string            `json:"friendlyName"`
	ManufacturerName  string            `json:"manufacturerName"`
	Description       string            `json:"description"`
	DisplayCategories []string          `json:"displayCategories"`
	Capabilities      []AlexaCapability `json:"capabilities"`
}

// AlexaCapability describes an Alexa interface supported by an endpoint.
type AlexaCapability struct {
	Type          string            `json:"type"`
	Interface     string            `json:"interface"`
	Version       string            `json:"version"`
	Instance      string            `json:"instance,omitempty"`
	Properties    *AlexaProperties  `json:"properties,omitempty"`
	Configuration map[string]any    `json:"configuration,omitempty"`
}

// AlexaProperties describes the properties for a capability.
type AlexaProperties struct {
	Supported           []AlexaPropertyName `json:"supported"`
	ProactivelyReported bool                `json:"proactivelyReported"`
	Retrievable         bool                `json:"retrievable"`
}

// AlexaPropertyName is a property name reference.
type AlexaPropertyName struct {
	Name string `json:"name"`
}

// AlexaProperty is a state property value for change/state reports.
type AlexaProperty struct {
	Namespace                 string `json:"namespace"`
	Name                      string `json:"name"`
	Instance                  string `json:"instance,omitempty"`
	Value                     any    `json:"value"`
	TimeOfSample              string `json:"timeOfSample"`
	UncertaintyInMilliseconds int    `json:"uncertaintyInMilliseconds"`
}

// ---------------------------------------------------------------------------
// Capability builder helpers
// ---------------------------------------------------------------------------

func cap(iface string, props ...string) AlexaCapability {
	c := AlexaCapability{Type: "AlexaInterface", Interface: iface, Version: "3"}
	if len(props) > 0 {
		supported := make([]AlexaPropertyName, len(props))
		for i, p := range props {
			supported[i] = AlexaPropertyName{Name: p}
		}
		c.Properties = &AlexaProperties{
			Supported:           supported,
			ProactivelyReported: true,
			Retrievable:         true,
		}
	}
	return c
}

func capWithInstance(iface, instance string, props ...string) AlexaCapability {
	c := cap(iface, props...)
	c.Instance = instance
	return c
}

func rangeCap(instance string, min, max float64) AlexaCapability {
	c := capWithInstance("Alexa.RangeController", instance, "rangeValue")
	c.Configuration = map[string]any{
		"supportedRange": map[string]any{
			"minimumValue": min,
			"maximumValue": max,
			"precision":    1,
		},
	}
	return c
}

func prop(namespace, name string, value any) AlexaProperty {
	return AlexaProperty{
		Namespace:                 namespace,
		Name:                     name,
		Value:                    value,
		TimeOfSample:             time.Now().UTC().Format(time.RFC3339),
		UncertaintyInMilliseconds: 500,
	}
}

func propWithInstance(namespace, name, instance string, value any) AlexaProperty {
	p := prop(namespace, name, value)
	p.Instance = instance
	return p
}

// ---------------------------------------------------------------------------
// ToAlexa: domain.Entity → AlexaEndpoint (for discovery)
// ---------------------------------------------------------------------------

func ToAlexa(entity domain.Entity) AlexaEndpoint {
	ep := AlexaEndpoint{
		EndpointID:       entity.Key(),
		FriendlyName:     entity.Name,
		ManufacturerName: "SlideBolt",
		Description:      entity.Type + " via SlideBolt",
		Capabilities:     []AlexaCapability{cap("Alexa")},
	}

	switch entity.Type {
	case "light":
		ep.DisplayCategories = []string{"LIGHT"}
		s, _ := entity.State.(domain.Light)
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.PowerController", "powerState"))
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.BrightnessController", "brightness"))
		if len(s.RGB) == 3 || len(s.HS) == 2 || len(s.XY) == 2 {
			ep.Capabilities = append(ep.Capabilities, cap("Alexa.ColorController", "color"))
		}
		if s.Temperature > 0 {
			ep.Capabilities = append(ep.Capabilities, cap("Alexa.ColorTemperatureController", "colorTemperatureInKelvin"))
		}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))

	case "switch":
		ep.DisplayCategories = []string{"SWITCH"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "cover":
		ep.DisplayCategories = []string{"EXTERIOR_BLIND"}
		ep.Capabilities = append(ep.Capabilities,
			rangeCap("Cover.Position", 0, 100),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "lock":
		ep.DisplayCategories = []string{"SMARTLOCK"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.LockController", "lockState"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "fan":
		ep.DisplayCategories = []string{"FAN"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			rangeCap("Fan.Speed", 0, 100),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "sensor":
		s, _ := entity.State.(domain.Sensor)
		if s.DeviceClass == "temperature" || s.Unit == "°C" || s.Unit == "°F" {
			ep.DisplayCategories = []string{"TEMPERATURE_SENSOR"}
			ep.Capabilities = append(ep.Capabilities,
				cap("Alexa.TemperatureSensor", "temperature"),
			)
		} else {
			ep.DisplayCategories = []string{"OTHER"}
		}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))

	case "binary_sensor":
		s, _ := entity.State.(domain.BinarySensor)
		if s.DeviceClass == "motion" {
			ep.DisplayCategories = []string{"MOTION_SENSOR"}
			ep.Capabilities = append(ep.Capabilities,
				cap("Alexa.MotionSensor", "detectionState"),
			)
		} else {
			ep.DisplayCategories = []string{"CONTACT_SENSOR"}
			ep.Capabilities = append(ep.Capabilities,
				cap("Alexa.ContactSensor", "detectionState"),
			)
		}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))

	case "climate":
		ep.DisplayCategories = []string{"THERMOSTAT"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.ThermostatController", "thermostatMode", "targetSetpoint"),
			cap("Alexa.TemperatureSensor", "temperature"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "button":
		ep.DisplayCategories = []string{"SCENE_TRIGGER"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.SceneController"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "number":
		s, _ := entity.State.(domain.Number)
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities,
			rangeCap("Number.Value", s.Min, s.Max),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "select":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities,
			capWithInstance("Alexa.ModeController", "Select.Mode", "mode"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "text":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))

	case "alarm":
		ep.DisplayCategories = []string{"SECURITY_PANEL"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.SecurityPanelController", "armState"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "camera":
		ep.DisplayCategories = []string{"CAMERA"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.CameraStreamController"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "valve":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			rangeCap("Valve.Position", 0, 100),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "siren":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "humidifier":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			rangeCap("Humidifier.Humidity", 0, 100),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "media_player":
		ep.DisplayCategories = []string{"STREAMING_DEVICE"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			cap("Alexa.PlaybackController"),
			cap("Alexa.Speaker", "volume", "muted"),
			cap("Alexa.InputController", "input"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "remote":
		ep.DisplayCategories = []string{"REMOTE"}
		ep.Capabilities = append(ep.Capabilities,
			cap("Alexa.PowerController", "powerState"),
			cap("Alexa.EndpointHealth", "connectivity"),
		)

	case "event":
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))

	default:
		ep.DisplayCategories = []string{"OTHER"}
		ep.Capabilities = append(ep.Capabilities, cap("Alexa.EndpointHealth", "connectivity"))
	}

	return ep
}

// ---------------------------------------------------------------------------
// AlexaState: domain.Entity → []AlexaProperty (for change/state reports)
// ---------------------------------------------------------------------------

func AlexaState(entity domain.Entity) []AlexaProperty {
	props := []AlexaProperty{
		prop("Alexa.EndpointHealth", "connectivity", map[string]string{"value": "OK"}),
	}

	switch entity.Type {
	case "light":
		s, _ := entity.State.(domain.Light)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.Power)))
		props = append(props, prop("Alexa.BrightnessController", "brightness", sbBrightnessToAlexa(s.Brightness)))
		if len(s.RGB) == 3 {
			h, sat, v := rgbToHSV(s.RGB[0], s.RGB[1], s.RGB[2])
			props = append(props, prop("Alexa.ColorController", "color", map[string]float64{
				"hue": h, "saturation": sat, "brightness": v,
			}))
		} else if len(s.HS) == 2 {
			props = append(props, prop("Alexa.ColorController", "color", map[string]float64{
				"hue": s.HS[0], "saturation": s.HS[1] / 100, "brightness": float64(s.Brightness) / 254,
			}))
		}
		if s.Temperature > 0 {
			props = append(props, prop("Alexa.ColorTemperatureController", "colorTemperatureInKelvin", s.Temperature))
		}

	case "switch":
		s, _ := entity.State.(domain.Switch)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.Power)))

	case "cover":
		s, _ := entity.State.(domain.Cover)
		props = append(props, propWithInstance("Alexa.RangeController", "rangeValue", "Cover.Position", s.Position))

	case "lock":
		s, _ := entity.State.(domain.Lock)
		v := "UNLOCKED"
		if s.Locked {
			v = "LOCKED"
		}
		props = append(props, prop("Alexa.LockController", "lockState", v))

	case "fan":
		s, _ := entity.State.(domain.Fan)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.Power)))
		props = append(props, propWithInstance("Alexa.RangeController", "rangeValue", "Fan.Speed", s.Percentage))

	case "sensor":
		s, _ := entity.State.(domain.Sensor)
		if s.DeviceClass == "temperature" || s.Unit == "°C" || s.Unit == "°F" {
			scale := "CELSIUS"
			if s.Unit == "°F" {
				scale = "FAHRENHEIT"
			}
			props = append(props, prop("Alexa.TemperatureSensor", "temperature", map[string]any{
				"value": s.Value, "scale": scale,
			}))
		}

	case "binary_sensor":
		s, _ := entity.State.(domain.BinarySensor)
		v := "NOT_DETECTED"
		if s.On {
			v = "DETECTED"
		}
		if s.DeviceClass == "motion" {
			props = append(props, prop("Alexa.MotionSensor", "detectionState", v))
		} else {
			props = append(props, prop("Alexa.ContactSensor", "detectionState", v))
		}

	case "climate":
		s, _ := entity.State.(domain.Climate)
		props = append(props, prop("Alexa.ThermostatController", "thermostatMode", alexaThermostatMode(s.HVACMode)))
		props = append(props, prop("Alexa.ThermostatController", "targetSetpoint", map[string]any{
			"value": s.Temperature, "scale": "CELSIUS",
		}))
		if s.CurrentTemperature > 0 {
			props = append(props, prop("Alexa.TemperatureSensor", "temperature", map[string]any{
				"value": s.CurrentTemperature, "scale": "CELSIUS",
			}))
		}

	case "number":
		s, _ := entity.State.(domain.Number)
		props = append(props, propWithInstance("Alexa.RangeController", "rangeValue", "Number.Value", s.Value))

	case "select":
		s, _ := entity.State.(domain.Select)
		props = append(props, propWithInstance("Alexa.ModeController", "mode", "Select.Mode", s.Option))

	case "alarm":
		s, _ := entity.State.(domain.Alarm)
		props = append(props, prop("Alexa.SecurityPanelController", "armState", alexaArmState(s.AlarmState)))

	case "valve":
		s, _ := entity.State.(domain.Valve)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.Position > 0)))
		props = append(props, propWithInstance("Alexa.RangeController", "rangeValue", "Valve.Position", s.Position))

	case "siren":
		s, _ := entity.State.(domain.Siren)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.IsOn)))

	case "humidifier":
		s, _ := entity.State.(domain.Humidifier)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.IsOn)))
		props = append(props, propWithInstance("Alexa.RangeController", "rangeValue", "Humidifier.Humidity", s.TargetHumidity))

	case "media_player":
		s, _ := entity.State.(domain.MediaPlayer)
		isOn := s.State != "" && s.State != "off" && s.State != "idle"
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(isOn)))
		props = append(props, prop("Alexa.Speaker", "volume", int(s.VolumeLevel*100)))
		props = append(props, prop("Alexa.Speaker", "muted", s.IsVolumeMuted))

	case "remote":
		s, _ := entity.State.(domain.Remote)
		props = append(props, prop("Alexa.PowerController", "powerState", powerValue(s.IsOn)))
	}

	return props
}

// ---------------------------------------------------------------------------
// FromAlexa: Alexa directive → SlideBolt domain command
// ---------------------------------------------------------------------------

func FromAlexa(entityType, namespace, name string, payload map[string]any) (any, error) {
	switch namespace {
	case "Alexa.PowerController":
		return fromPower(entityType, name)

	case "Alexa.BrightnessController":
		if name == "SetBrightness" {
			b := toInt(payload["brightness"])
			sb := alexaBrightnessToSB(b)
			return domain.LightSetBrightness{Brightness: sb}, nil
		}

	case "Alexa.ColorController":
		if name == "SetColor" {
			color, _ := payload["color"].(map[string]any)
			hue := toFloat64(color["hue"])
			sat := toFloat64(color["saturation"])
			return domain.LightSetHS{Hue: hue, Saturation: sat * 100}, nil
		}

	case "Alexa.ColorTemperatureController":
		if name == "SetColorTemperature" {
			kelvin := toInt(payload["colorTemperatureInKelvin"])
			return domain.LightSetColorTemp{Mireds: kelvin}, nil
		}

	case "Alexa.LockController":
		switch name {
		case "Lock":
			return domain.LockLock{}, nil
		case "Unlock":
			return domain.LockUnlock{}, nil
		}

	case "Alexa.ThermostatController":
		switch name {
		case "SetTargetTemperature":
			setpoint, _ := payload["targetSetpoint"].(map[string]any)
			temp := toFloat64(setpoint["value"])
			return domain.ClimateSetTemperature{Temperature: temp}, nil
		case "SetThermostatMode":
			mode, _ := payload["thermostatMode"].(map[string]any)
			return domain.ClimateSetMode{HVACMode: sbThermostatMode(toString(mode["value"]))}, nil
		}

	case "Alexa.RangeController":
		instance := toString(payload["instance"])
		if instance == "" {
			instance = toString(payload["_instance"])
		}
		value := toFloat64(payload["rangeValue"])
		switch instance {
		case "Cover.Position":
			return domain.CoverSetPosition{Position: int(value)}, nil
		case "Fan.Speed":
			return domain.FanSetSpeed{Percentage: int(value)}, nil
		case "Number.Value":
			return domain.NumberSetValue{Value: value}, nil
		case "Valve.Position":
			return domain.ValveSetPosition{Position: int(value)}, nil
		case "Humidifier.Humidity":
			return domain.HumidifierSetHumidity{Humidity: int(value)}, nil
		}

	case "Alexa.ModeController":
		instance := toString(payload["instance"])
		if instance == "" {
			instance = toString(payload["_instance"])
		}
		if instance == "Select.Mode" {
			return domain.SelectOption{Option: toString(payload["mode"])}, nil
		}

	case "Alexa.PlaybackController":
		switch name {
		case "Play":
			return domain.MediaPlay{}, nil
		case "Pause":
			return domain.MediaPause{}, nil
		case "Stop":
			return domain.MediaStop{}, nil
		case "Next":
			return domain.MediaNextTrack{}, nil
		case "Previous":
			return domain.MediaPreviousTrack{}, nil
		}

	case "Alexa.Speaker":
		switch name {
		case "SetVolume":
			vol := toFloat64(payload["volume"])
			return domain.MediaSetVolume{VolumeLevel: vol / 100}, nil
		case "SetMute":
			return domain.MediaMute{Mute: toBool(payload["mute"])}, nil
		}

	case "Alexa.InputController":
		if name == "SelectInput" {
			return domain.MediaSelectSource{Source: toString(payload["input"])}, nil
		}

	case "Alexa.SceneController":
		if name == "Activate" {
			return domain.ButtonPress{}, nil
		}

	case "Alexa.SecurityPanelController":
		switch name {
		case "Arm":
			armState := toString(payload["armState"])
			switch armState {
			case "ARMED_AWAY":
				return domain.AlarmArmAway{}, nil
			case "ARMED_NIGHT":
				return domain.AlarmArmNight{}, nil
			default:
				return domain.AlarmArmHome{}, nil
			}
		case "Disarm":
			return domain.AlarmDisarm{}, nil
		}
	}

	return nil, fmt.Errorf("unsupported directive: namespace=%q name=%q entityType=%q", namespace, name, entityType)
}

// fromPower maps Alexa.PowerController TurnOn/TurnOff to the correct domain
// command based on entity type.
func fromPower(entityType, name string) (any, error) {
	switch name {
	case "TurnOn":
		switch entityType {
		case "light":
			return domain.LightTurnOn{}, nil
		case "switch":
			return domain.SwitchTurnOn{}, nil
		case "fan":
			return domain.FanTurnOn{}, nil
		case "siren":
			return domain.SirenTurnOn{}, nil
		case "humidifier":
			return domain.HumidifierTurnOn{}, nil
		case "valve":
			return domain.ValveOpen{}, nil
		case "media_player":
			return domain.MediaPlay{}, nil
		case "remote":
			return domain.RemoteTurnOn{}, nil
		}
	case "TurnOff":
		switch entityType {
		case "light":
			return domain.LightTurnOff{}, nil
		case "switch":
			return domain.SwitchTurnOff{}, nil
		case "fan":
			return domain.FanTurnOff{}, nil
		case "siren":
			return domain.SirenTurnOff{}, nil
		case "humidifier":
			return domain.HumidifierTurnOff{}, nil
		case "valve":
			return domain.ValveClose{}, nil
		case "media_player":
			return domain.MediaStop{}, nil
		case "remote":
			return domain.RemoteTurnOff{}, nil
		}
	}
	return nil, fmt.Errorf("unsupported: PowerController.%s for %s", name, entityType)
}

// ---------------------------------------------------------------------------
// Brightness scaling: SB 0-254 ↔ Alexa 0-100
// ---------------------------------------------------------------------------

func sbBrightnessToAlexa(sb int) int {
	if sb <= 0 {
		return 0
	}
	return int(math.Round(float64(sb) * 100 / 254))
}

func alexaBrightnessToSB(alexa int) int {
	if alexa <= 0 {
		return 0
	}
	return int(math.Round(float64(alexa) * 254 / 100))
}

// ---------------------------------------------------------------------------
// Color conversion: RGB ↔ HSV
// ---------------------------------------------------------------------------

// rgbToHSV converts RGB (0-255) to HSV (hue 0-360, saturation 0-1, value 0-1).
func rgbToHSV(r, g, b int) (float64, float64, float64) {
	rf := float64(r) / 255
	gf := float64(g) / 255
	bf := float64(b) / 255

	max := math.Max(rf, math.Max(gf, bf))
	min := math.Min(rf, math.Min(gf, bf))
	d := max - min

	var h float64
	switch {
	case d == 0:
		h = 0
	case max == rf:
		h = math.Mod((gf-bf)/d, 6)
	case max == gf:
		h = (bf-rf)/d + 2
	case max == bf:
		h = (rf-gf)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}

	s := 0.0
	if max != 0 {
		s = d / max
	}
	return math.Round(h*100) / 100, math.Round(s*10000) / 10000, math.Round(max*10000) / 10000
}

// hsvToRGB converts HSV (hue 0-360, saturation 0-1, value 0-1) to RGB (0-255).
func hsvToRGB(h, s, v float64) (int, int, int) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c

	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}
	return int(math.Round((r1 + m) * 255)),
		int(math.Round((g1 + m) * 255)),
		int(math.Round((b1 + m) * 255))
}

// ---------------------------------------------------------------------------
// Thermostat mode mapping: SB ↔ Alexa
// ---------------------------------------------------------------------------

func alexaThermostatMode(sbMode string) string {
	switch sbMode {
	case "heat":
		return "HEAT"
	case "cool":
		return "COOL"
	case "auto":
		return "AUTO"
	case "off":
		return "OFF"
	case "heat_cool":
		return "AUTO"
	default:
		return "OFF"
	}
}

func sbThermostatMode(alexaMode string) string {
	switch alexaMode {
	case "HEAT":
		return "heat"
	case "COOL":
		return "cool"
	case "AUTO":
		return "auto"
	case "OFF":
		return "off"
	default:
		return "off"
	}
}

// ---------------------------------------------------------------------------
// Alarm state mapping: SB ↔ Alexa
// ---------------------------------------------------------------------------

func alexaArmState(sbState string) string {
	switch sbState {
	case "armed_home":
		return "ARMED_STAY"
	case "armed_away":
		return "ARMED_AWAY"
	case "armed_night":
		return "ARMED_NIGHT"
	case "disarmed":
		return "DISARMED"
	default:
		return "DISARMED"
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func powerValue(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}

// ---------------------------------------------------------------------------
// Decode: raw protocol → domain state (identity codec)
// ---------------------------------------------------------------------------

func Decode(entityType string, raw json.RawMessage) (any, bool) {
	switch entityType {
	case "light":
		return decodeLight(raw)
	case "switch":
		return decodeSwitch(raw)
	case "cover":
		return decodeCover(raw)
	case "lock":
		return decodeLock(raw)
	case "fan":
		return decodeFan(raw)
	case "sensor":
		return decodeSensor(raw)
	case "binary_sensor":
		return decodeBinarySensor(raw)
	case "climate":
		return decodeClimate(raw)
	case "button":
		return decodeButton(raw)
	case "number":
		return decodeNumber(raw)
	case "select":
		return decodeSelect(raw)
	case "text":
		return decodeText(raw)
	case "alarm":
		return decodeAlarm(raw)
	case "camera":
		return decodeCamera(raw)
	case "valve":
		return decodeValve(raw)
	case "siren":
		return decodeSiren(raw)
	case "humidifier":
		return decodeHumidifier(raw)
	case "media_player":
		return decodeMediaPlayer(raw)
	case "remote":
		return decodeRemote(raw)
	case "event":
		return decodeEvent(raw)
	default:
		return nil, false
	}
}

// Encode converts a SlideBolt domain command into a raw protocol payload.
func Encode(cmd any, internal json.RawMessage) (json.RawMessage, error) {
	switch c := cmd.(type) {
	case domain.LightTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.LightTurnOff:
		return json.Marshal(c)
	case domain.LightSetBrightness:
		if c.Brightness < 0 || c.Brightness > 254 {
			return nil, fmt.Errorf("translate: brightness %d out of range [0,254]", c.Brightness)
		}
		return json.Marshal(c)
	case domain.LightSetColorTemp:
		if c.Mireds < 153 || c.Mireds > 500 {
			return nil, fmt.Errorf("translate: mireds %d out of range [153,500]", c.Mireds)
		}
		return json.Marshal(c)
	case domain.LightSetRGB:
		for name, v := range map[string]int{"r": c.R, "g": c.G, "b": c.B} {
			if v < 0 || v > 255 {
				return nil, fmt.Errorf("translate: %s value %d out of range [0,255]", name, v)
			}
		}
		return json.Marshal(c)
	case domain.LightSetRGBW:
		for name, v := range map[string]int{"r": c.R, "g": c.G, "b": c.B, "w": c.W} {
			if v < 0 || v > 255 {
				return nil, fmt.Errorf("translate: %s value %d out of range [0,255]", name, v)
			}
		}
		return json.Marshal(c)
	case domain.LightSetRGBWW:
		for name, v := range map[string]int{"r": c.R, "g": c.G, "b": c.B, "cw": c.CW, "ww": c.WW} {
			if v < 0 || v > 255 {
				return nil, fmt.Errorf("translate: %s value %d out of range [0,255]", name, v)
			}
		}
		return json.Marshal(c)
	case domain.LightSetHS:
		if c.Hue < 0 || c.Hue > 360 {
			return nil, fmt.Errorf("translate: hue %.2f out of range [0,360]", c.Hue)
		}
		if c.Saturation < 0 || c.Saturation > 100 {
			return nil, fmt.Errorf("translate: saturation %.2f out of range [0,100]", c.Saturation)
		}
		return json.Marshal(c)
	case domain.LightSetXY:
		if c.X < 0 || c.X > 1 {
			return nil, fmt.Errorf("translate: x %.4f out of range [0,1]", c.X)
		}
		if c.Y < 0 || c.Y > 1 {
			return nil, fmt.Errorf("translate: y %.4f out of range [0,1]", c.Y)
		}
		return json.Marshal(c)
	case domain.LightSetWhite:
		if c.White < 0 || c.White > 254 {
			return nil, fmt.Errorf("translate: white %d out of range [0,254]", c.White)
		}
		return json.Marshal(c)
	case domain.LightSetEffect:
		if c.Effect == "" {
			return nil, fmt.Errorf("translate: effect must not be empty")
		}
		return json.Marshal(c)
	case domain.SwitchTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.SwitchTurnOff:
		return json.Marshal(map[string]any{"state": "OFF"})
	case domain.SwitchToggle:
		return json.Marshal(map[string]any{"state": "TOGGLE"})
	case domain.FanTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.FanTurnOff:
		return json.Marshal(map[string]any{"state": "OFF"})
	case domain.FanSetSpeed:
		if c.Percentage < 0 || c.Percentage > 100 {
			return nil, fmt.Errorf("translate: fan percentage %d out of range 0-100", c.Percentage)
		}
		return json.Marshal(c)
	case domain.CoverOpen:
		return json.Marshal(map[string]any{"state": "OPEN"})
	case domain.CoverClose:
		return json.Marshal(map[string]any{"state": "CLOSE"})
	case domain.CoverSetPosition:
		if c.Position < 0 || c.Position > 100 {
			return nil, fmt.Errorf("translate: cover position %d out of range 0-100", c.Position)
		}
		return json.Marshal(c)
	case domain.LockLock:
		return json.Marshal(map[string]any{"state": "LOCK"})
	case domain.LockUnlock:
		return json.Marshal(c)
	case domain.ButtonPress:
		return json.Marshal(map[string]any{"action": "PRESS"})
	case domain.NumberSetValue:
		return json.Marshal(c)
	case domain.SelectOption:
		if c.Option == "" {
			return nil, fmt.Errorf("translate: select option must not be empty")
		}
		return json.Marshal(c)
	case domain.TextSetValue:
		return json.Marshal(c)
	case domain.ClimateSetMode:
		if c.HVACMode == "" {
			return nil, fmt.Errorf("translate: climate hvac_mode must not be empty")
		}
		return json.Marshal(c)
	case domain.ClimateSetTemperature:
		return json.Marshal(c)
	case domain.AlarmArmHome:
		return json.Marshal(c)
	case domain.AlarmArmAway:
		return json.Marshal(c)
	case domain.AlarmArmNight:
		return json.Marshal(c)
	case domain.AlarmDisarm:
		return json.Marshal(c)
	case domain.SirenTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.SirenTurnOff:
		return json.Marshal(map[string]any{"state": "OFF"})
	case domain.HumidifierTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.HumidifierTurnOff:
		return json.Marshal(map[string]any{"state": "OFF"})
	case domain.HumidifierSetHumidity:
		return json.Marshal(c)
	case domain.HumidifierSetMode:
		return json.Marshal(c)
	case domain.ValveOpen:
		return json.Marshal(map[string]any{"state": "OPEN"})
	case domain.ValveClose:
		return json.Marshal(map[string]any{"state": "CLOSE"})
	case domain.ValveSetPosition:
		return json.Marshal(c)
	case domain.MediaPlay:
		return json.Marshal(map[string]any{"state": "PLAY"})
	case domain.MediaPause:
		return json.Marshal(map[string]any{"state": "PAUSE"})
	case domain.MediaStop:
		return json.Marshal(map[string]any{"state": "STOP"})
	case domain.MediaSetVolume:
		return json.Marshal(c)
	case domain.MediaMute:
		return json.Marshal(c)
	case domain.MediaNextTrack:
		return json.Marshal(map[string]any{"action": "NEXT"})
	case domain.MediaPreviousTrack:
		return json.Marshal(map[string]any{"action": "PREVIOUS"})
	case domain.MediaSelectSource:
		return json.Marshal(c)
	case domain.RemoteTurnOn:
		return json.Marshal(map[string]any{"state": "ON"})
	case domain.RemoteTurnOff:
		return json.Marshal(map[string]any{"state": "OFF"})
	case domain.RemoteSendCommand:
		return json.Marshal(c)
	case domain.CameraRecordStart:
		return json.Marshal(map[string]any{"action": "RECORD_START"})
	case domain.CameraRecordStop:
		return json.Marshal(map[string]any{"action": "RECORD_STOP"})
	case domain.CameraEnableMotion:
		return json.Marshal(map[string]any{"action": "ENABLE_MOTION"})
	case domain.CameraDisableMotion:
		return json.Marshal(map[string]any{"action": "DISABLE_MOTION"})
	default:
		return nil, fmt.Errorf("translate: unsupported command type %T", cmd)
	}
}

// ---------------------------------------------------------------------------
// Decode: per-type (raw protocol → domain state, identity codec)
// ---------------------------------------------------------------------------

func decodeLight(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Light
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if s.Brightness < 0 {
		s.Brightness = 0
	}
	if s.Brightness > 254 {
		s.Brightness = 254
	}
	return s, true
}

func decodeSwitch(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Switch
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeCover(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Cover
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if s.Position < 0 {
		s.Position = 0
	}
	if s.Position > 100 {
		s.Position = 100
	}
	return s, true
}

func decodeLock(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Lock
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeFan(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Fan
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if s.Percentage < 0 {
		s.Percentage = 0
	}
	if s.Percentage > 100 {
		s.Percentage = 100
	}
	return s, true
}

func decodeSensor(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Sensor
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeBinarySensor(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.BinarySensor
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeClimate(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Climate
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeButton(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Button
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeNumber(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Number
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeSelect(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Select
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeText(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Text
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeAlarm(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Alarm
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeCamera(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Camera
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeValve(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Valve
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	if s.Position < 0 {
		s.Position = 0
	}
	if s.Position > 100 {
		s.Position = 100
	}
	return s, true
}

func decodeSiren(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Siren
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeHumidifier(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Humidifier
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeMediaPlayer(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.MediaPlayer
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeRemote(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Remote
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

func decodeEvent(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s domain.Event
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, false
	}
	return s, true
}

// ---------------------------------------------------------------------------
// Type conversion helpers
// ---------------------------------------------------------------------------

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}
