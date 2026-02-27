package main

import (
	"fmt"
	"math"
	"time"

	"github.com/slidebolt/sdk-entities/light"
	"github.com/slidebolt/sdk-types"
)

// AlexaEventFactory generates Alexa-compliant JSON event structures.
type AlexaEventFactory struct{}

func NewAlexaEventFactory() *AlexaEventFactory {
	return &AlexaEventFactory{}
}

func (f *AlexaEventFactory) CreateDeleteReport(endpointID string) map[string]any {
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

func (f *AlexaEventFactory) CreateAddOrUpdateReport(endpoint map[string]any) map[string]any {
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

func (f *AlexaEventFactory) newMsgID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// --- Alexa endpoint building ---

func buildAlexaEndpoint(dev types.Device, entities []types.Entity) map[string]any {
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
	}

	name := dev.Name()
	if name == "" {
		name = dev.ID
	}

	alexaCaps := []map[string]any{
		{"type": "AlexaInterface", "interface": "Alexa", "version": "3"},
		{
			"type": "AlexaInterface", "interface": "Alexa.PowerController", "version": "3",
			"properties": map[string]any{
				"supported": []map[string]any{{"name": "powerState"}}, "proactivelyReported": true, "retrievable": true,
			},
		},
	}
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

	return map[string]any{
		"endpointId":        dev.ID,
		"friendlyName":      name,
		"manufacturerName":  "SlideBolt",
		"description":       "SlideBolt device",
		"displayCategories": []string{display},
		"capabilities":      alexaCaps,
	}
}

// --- Alexa state translation ---

func stateFromEntity(ent types.Entity) map[string]any {
	// The reported state is in ent.Data.Reported.
	// (Actually we should parse the reported state based on domain).
	// For now, let's assume it has power, brightness, r, g, b, kelvin.
	// This is a simplification.
	return nil // To be implemented with real parsing
}

func stateFromProps(state map[string]interface{}) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any

	if v, ok := state["power"].(bool); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.PowerController", "name": "powerState",
			"value": boolToOnOff(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := toFloat(state["brightness"]); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.BrightnessController", "name": "brightness",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if v, ok := toFloat(firstNonNil(state["temperature"], state["kelvin"])); ok {
		props = append(props, map[string]any{
			"namespace": "Alexa.ColorTemperatureController", "name": "colorTemperatureInKelvin",
			"value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500,
		})
	}
	if r, rok := toFloat(state["r"]); rok {
		if g, gok := toFloat(state["g"]); gok {
			if b, bok := toFloat(state["b"]); bok {
				h, s, bri := rgbToHSV(r/255.0, g/255.0, b/255.0)
				props = append(props, map[string]any{
					"namespace": "Alexa.ColorController", "name": "color",
					"value":        map[string]any{"hue": h, "saturation": s, "brightness": bri},
					"timeOfSample": ts, "uncertaintyInMilliseconds": 500,
				})
			}
		}
	}

	if len(props) == 0 {
		return nil
	}
	return map[string]any{"properties": props}
}

func stateFromPrimitive(vals map[string]any) map[string]any {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	var props []map[string]any
	if v, ok := vals["powerState"].(string); ok && v != "" {
		props = append(props, map[string]any{"namespace": "Alexa.PowerController", "name": "powerState", "value": v, "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := toFloat(vals["brightness"]); ok {
		props = append(props, map[string]any{"namespace": "Alexa.BrightnessController", "name": "brightness", "value": int(v), "timeOfSample": ts, "uncertaintyInMilliseconds": 500})
	}
	if v, ok := toFloat(vals["colorTemperatureInKelvin"]); ok {
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

// --- Math helpers ---

func boolToOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func rgbToHSV(r, g, b float64) (float64, float64, float64) {
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

func hsvToRGB(h, s, v float64) (int, int, int) {
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

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func coerceFloat(v any) float64 {
	n, _ := toFloat(v)
	return n
}

func toFloat(v any) (float64, bool) {
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

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
