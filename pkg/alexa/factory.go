package alexa

import (
	"fmt"
	"math"
	"time"

	"github.com/slidebolt/sdk-entities/light"
	"github.com/slidebolt/sdk-types"
)

// EventFactory generates Alexa-compliant JSON event structures.
type EventFactory struct{}

// NewEventFactory creates a new EventFactory instance.
func NewEventFactory() *EventFactory {
	return &EventFactory{}
}

// CreateDeleteReport creates an Alexa DeleteReport event.
func (f *EventFactory) CreateDeleteReport(endpointID string) map[string]any {
	return map[string]any{
		"event": map[string]any{
			"header": map[string]any{
				"namespace":      "Alexa.Discovery",
				"name":           "DeleteReport",
				"messageId":      f.newMsgID(),
				"payloadVersion": "3",
			},
			"payload": map[string]any{
				"endpoints": []map[string]any{
					{"endpointId": endpointID},
				},
			},
		},
	}
}

// CreateAddOrUpdateReport creates an Alexa AddOrUpdateReport event.
func (f *EventFactory) CreateAddOrUpdateReport(endpoint map[string]any) map[string]any {
	return map[string]any{
		"event": map[string]any{
			"header": map[string]any{
				"namespace":      "Alexa.Discovery",
				"name":           "AddOrUpdateReport",
				"messageId":      f.newMsgID(),
				"payloadVersion": "3",
			},
			"payload": map[string]any{
				"endpoints": []map[string]any{endpoint},
			},
		},
	}
}

func (f *EventFactory) newMsgID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// BuildEndpoint creates an Alexa endpoint from a Slidebolt device and its entities.
func BuildEndpoint(dev types.Device, entities []types.Entity) map[string]any {
	if len(entities) == 0 {
		return nil
	}
	primary := entities[0]

	// Determine capabilities based on actions and domain.
	caps := make(map[string]bool)
	for _, a := range primary.Actions {
		caps[a] = true
	}

	display := "SWITCH"
	if primary.Domain == "light" || caps[light.ActionSetBrightness] || caps[light.ActionSetRGB] || caps[light.ActionSetTemperature] {
		display = "LIGHT"
	} else if primary.Domain == "sensor" || primary.Domain == "climate" {
		// Check for temperature/humidity sensors
		if caps["read_temperature"] || caps["read_humidity"] {
			display = "THERMOSTAT"
		}
	} else if primary.Domain == "lock" {
		display = "SMARTLOCK"
	} else if primary.Domain == "cover" {
		display = "DOOR"
	}

	name := dev.Name()
	if name == "" {
		name = dev.ID
	}

	alexaCaps := []map[string]any{
		{"type": "AlexaInterface", "interface": "Alexa", "version": "3"},
	}

	// PowerController for most devices except pure sensors
	if primary.Domain != "sensor" && primary.Domain != "binary_sensor" {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.PowerController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "powerState"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}

	// Light capabilities
	if caps[light.ActionSetBrightness] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.BrightnessController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "brightness"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}
	if caps[light.ActionSetRGB] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.ColorController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "color"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}
	if caps[light.ActionSetTemperature] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.ColorTemperatureController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "colorTemperatureInKelvin"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}

	// Sensor capabilities
	if caps["read_temperature"] || primary.Domain == "climate" {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.TemperatureSensor", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "temperature"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}
	if caps["read_humidity"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.RelativeHumiditySensor", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "relativeHumidity"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}

	// Lock capabilities
	if primary.Domain == "lock" || caps["lock"] || caps["unlock"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.LockController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "lockState"}}, "proactivelyReported": true, "retrievable": true,
			},
		})
	}

	// Cover capabilities (using ModeController for open/close/position)
	if primary.Domain == "cover" || caps["open_cover"] || caps["close_cover"] || caps["set_cover_position"] {
		alexaCaps = append(alexaCaps, map[string]any{
			"type": "AlexaInterface", "interface": "Alexa.ModeController", "version": "3",
			"instance": "Cover.Position",
			"properties": map[string]any{
				"supported":           []map[string]any{{"name": "mode"}},
				"proactivelyReported": true,
				"retrievable":         true,
			},
			"capabilityResources": map[string]any{
				"friendlyNames": []any{
					map[string]any{"@type": "text", "value": map[string]any{"text": "Position", "locale": "en-US"}},
				},
			},
			"configuration": map[string]any{
				"ordered": true,
				"supportedModes": []any{
					map[string]any{
						"value": "Position.Open",
						"modeResources": map[string]any{
							"friendlyNames": []any{
								map[string]any{"@type": "text", "value": map[string]any{"text": "Open", "locale": "en-US"}},
							},
						},
					},
					map[string]any{
						"value": "Position.Closed",
						"modeResources": map[string]any{
							"friendlyNames": []any{
								map[string]any{"@type": "text", "value": map[string]any{"text": "Closed", "locale": "en-US"}},
							},
						},
					},
					map[string]any{
						"value": "Position.Partial",
						"modeResources": map[string]any{
							"friendlyNames": []any{
								map[string]any{"@type": "text", "value": map[string]any{"text": "Partial", "locale": "en-US"}},
							},
						},
					},
				},
			},
		})
	}

	return map[string]any{
		"endpointId":        dev.ID,
		"friendlyName":      name,
		"manufacturerName":  "SlideBolt",
		"description":       "SlideBolt device",
		"displayCategories": []string{display},
		"capabilities":      alexaCaps,
	}
}

// StateFromProps converts state properties to Alexa state format.
func StateFromProps(state map[string]interface{}) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any

	if v, ok := state["power"].(bool); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.PowerController", "name": "powerState",
			"value": BoolToOnOff(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := ToFloat(state["brightness"]); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.BrightnessController", "name": "brightness",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := ToFloat(FirstNonNil(state["temperature"], state["kelvin"])); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.ColorTemperatureController", "name": "colorTemperatureInKelvin",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if r, rok := ToFloat(state["r"]); rok {
		if g, gok := ToFloat(state["g"]); gok {
			if b, bok := ToFloat(state["b"]); bok {
				h, s, bri := RGBToHSV(r/255.0, g/255.0, b/255.0)
				props = append(props, map[string]any{
					"namespace": "Alexa.ColorController", "name": "color",
					"value":        map[string]any{"hue": h, "saturation": s, "brightness": bri},
					"timeOfSample": ts, "uncertaintyInMilliseconds": 500,
				})
			}
		}
	}

	// Temperature sensor (in Celsius for Alexa)
	if v, ok := ToFloat(state["temperature_c"]); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.TemperatureSensor", "name": "temperature",
			"value": map[string]any{
				"value": v,
				"scale": "CELSIUS",
			},
			"timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}

	// Humidity sensor
	if v, ok := ToFloat(state["humidity"]); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.RelativeHumiditySensor", "name": "relativeHumidity",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}

	// Lock state
	if v, ok := state["locked"].(bool); ok {
		lockState := "UNLOCKED"
		if v {
			lockState = "LOCKED"
		}
		props = append(props, map[string]any{
			"namespace": "Alexa.LockController", "name": "lockState",
			"value": lockState, "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}

	// Cover position (mapped to mode)
	if v, ok := ToFloat(state["cover_position"]); ok {
		mode := "Position.Partial"
		if v >= 90 {
			mode = "Position.Open"
		} else if v <= 10 {
			mode = "Position.Closed"
		}
		props = append(props, map[string]any{
			"namespace": "Alexa.ModeController", "name": "mode",
			"instance": "Cover.Position",
			"value":    mode, "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}

	if len(props) == 0 {
		return nil
	}
	return map[string]any{"properties": props}
}

// StateFromPrimitive converts primitive values to Alexa state format.
func StateFromPrimitive(vals map[string]any) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any
	if v, ok := vals["powerState"].(string); ok && v != "" {
		props = append(props, map[string]any{"namespace": "Alexa.PowerController", "name": "powerState", "value": v, "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := ToFloat(vals["brightness"]); ok {
		props = append(props, map[string]any{"namespace": "Alexa.BrightnessController", "name": "brightness", "value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := ToFloat(vals["colorTemperatureInKelvin"]); ok {
		props = append(props, map[string]any{"namespace": "Alexa.ColorTemperatureController", "name": "colorTemperatureInKelvin", "value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if c, ok := vals["color"].(map[string]any); ok {
		props = append(props, map[string]any{"namespace": "Alexa.ColorController", "name": "color", "value": c, "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if len(props) == 0 {
		return nil
	}
	return map[string]any{"properties": props}
}

// BoolToOnOff converts a bool to "ON"/"OFF" string.
func BoolToOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

// RGBToHSV converts RGB values to HSV.
func RGBToHSV(r, g, b float64) (float64, float64, float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	delta := mx - mn
	var h float64
	switch {
	case delta == 0:
		h = 0
	case mx == r:
		h = 60 * math.Mod((g-b)/delta, 6)
	case mx == g:
		h = 60 * (((b - r) / delta) + 2)
	default:
		h = 60 * (((r - g) / delta) + 4)
	}
	if h < 0 {
		h += 360
	}
	var s float64
	if mx != 0 {
		s = delta / mx
	}
	return h, s, mx
}

// HSVToRGB converts HSV values to RGB.
func HSVToRGB(h, s, v float64) (int, int, int) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60.0, 2)-1))
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
	clamp := func(v float64) int {
		i := int(math.Round(v * 255))
		if i < 0 {
			return 0
		}
		if i > 255 {
			return 255
		}
		return i
	}
	return clamp(r1 + m), clamp(g1 + m), clamp(b1 + m)
}

// ClampFloat clamps a float value between min and max.
func ClampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// CoerceFloat converts a value to float64, returning 0 if not possible.
func CoerceFloat(v any) float64 {
	n, _ := ToFloat(v)
	return n
}

// ToFloat attempts to convert a value to float64.
func ToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case uint32:
		return float64(n), true
	}
	return 0, false
}

// FirstNonNil returns the first non-nil value from the provided list.
func FirstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
