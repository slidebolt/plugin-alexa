//go:build integration

// End-to-end integration tests for the Alexa relay WebSocket API.
// These tests connect to the live AWS WebSocket endpoint and exercise
// the full relay round-trip: register → device_upsert → state_update →
// list_devices → device_delete.
//
// Run with:
//
//	go test -tags integration -v -count=1 -timeout 60s \
//	  ./cmd/plugin-alexa/
//
// Required environment variables (or .env.local):
//
//	ALEXA_CLIENT_ID      Client ID registered in SldBltData-v1-prod
//	ALEXA_CLIENT_SECRET  Raw secret for the client
//	ALEXA_RELAY_TOKEN    Static connect token for the WebSocket authorizer
//	ALEXA_WS_URL         WebSocket endpoint (e.g. wss://xxx.execute-api.us-east-1.amazonaws.com/prod)
package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	domain "github.com/slidebolt/sb-domain"
	translate "github.com/slidebolt/plugin-alexa/internal/translate"
)

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

func envOrFile(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	// Try loading from .env.local in the project root.
	return ""
}

func loadTestEnv(t *testing.T) (wsURL, relayToken, clientID, clientSecret string) {
	t.Helper()
	// Try to source .env.local if env vars are not set.
	if os.Getenv("ALEXA_WS_URL") == "" {
		loadDotEnv()
	}
	wsURL = os.Getenv("ALEXA_WS_URL")
	relayToken = os.Getenv("ALEXA_RELAY_TOKEN")
	clientID = os.Getenv("ALEXA_CLIENT_ID")
	clientSecret = os.Getenv("ALEXA_CLIENT_SECRET")

	if wsURL == "" || relayToken == "" || clientID == "" || clientSecret == "" {
		t.Skip("missing ALEXA_WS_URL, ALEXA_RELAY_TOKEN, ALEXA_CLIENT_ID, or ALEXA_CLIENT_SECRET")
	}
	return
}

// loadDotEnv is a minimal .env.local loader — no external deps.
func loadDotEnv() {
	for _, name := range []string{".env.local", "../../.env.local"} {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && os.Getenv(parts[0]) == "" {
				os.Setenv(parts[0], parts[1])
			}
		}
		return
	}
}

// ---------------------------------------------------------------------------
// WebSocket helpers
// ---------------------------------------------------------------------------

type wsConn struct {
	*websocket.Conn
	t *testing.T
}

func dial(t *testing.T, wsURL, relayToken string) *wsConn {
	t.Helper()
	u, err := url.Parse(wsURL)
	if err != nil {
		t.Fatalf("parse ws url: %v", err)
	}
	q := u.Query()
	q.Set("connectToken", relayToken)
	u.RawQuery = q.Encode()

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &wsConn{Conn: conn, t: t}
}

func (c *wsConn) send(msg map[string]any) {
	c.t.Helper()
	if err := c.WriteJSON(msg); err != nil {
		c.t.Fatalf("ws send: %v", err)
	}
}

func (c *wsConn) recv() map[string]any {
	c.t.Helper()
	c.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	var msg map[string]any
	if err := c.ReadJSON(&msg); err != nil {
		c.t.Fatalf("ws recv: %v", err)
	}
	return msg
}

func (c *wsConn) register(clientID, secret string) {
	c.t.Helper()
	c.send(map[string]any{
		"action":   "register",
		"clientId": clientID,
		"secret":   secret,
	})
	resp := c.recv()
	if resp["ok"] != true {
		c.t.Fatalf("register failed: %v", resp)
	}
	c.t.Logf("registered: connectionId=%v", resp["connectionId"])
}

// ---------------------------------------------------------------------------
// Test entity builders
// ---------------------------------------------------------------------------

func testEntity(typ, id, name string, state any) domain.Entity {
	return domain.Entity{
		Plugin:   "plugin-alexa",
		DeviceID: "e2e-device",
		ID:       id,
		Type:     typ,
		Name:     name,
		State:    state,
		Labels:   map[string][]string{"PluginAlexa": {"true"}},
	}
}

func endpointWithState(entity domain.Entity) (translate.AlexaEndpoint, []translate.AlexaProperty) {
	ep := translate.ToAlexa(entity)
	props := translate.AlexaState(entity)
	return ep, props
}

func alexaStatePayload(props []translate.AlexaProperty) map[string]any {
	arr := make([]map[string]any, len(props))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, p := range props {
		arr[i] = map[string]any{
			"namespace":                 p.Namespace,
			"name":                      p.Name,
			"value":                     p.Value,
			"timeOfSample":              now,
			"uncertaintyInMilliseconds": 500,
		}
		if p.Instance != "" {
			arr[i]["instance"] = p.Instance
		}
	}
	return map[string]any{"properties": arr}
}

// endpointJSON converts an AlexaEndpoint to a map for the wire.
func endpointJSON(ep translate.AlexaEndpoint) map[string]any {
	b, _ := json.Marshal(ep)
	var m map[string]any
	json.Unmarshal(b, &m) //nolint:errcheck
	return m
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestIntegration_ConnectAndRegister verifies basic WebSocket handshake.
func TestIntegration_ConnectAndRegister(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)
}

// TestIntegration_Keepalive sends a keepalive and expects an ack.
func TestIntegration_Keepalive(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	conn.send(map[string]any{"action": "keepalive"})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("keepalive failed: %v", resp)
	}
	t.Logf("keepalive ts=%v", resp["ts"])
}

// TestIntegration_DeviceUpsertAndList creates a device, lists, verifies it exists.
func TestIntegration_DeviceUpsertAndList(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entity := testEntity("light", "e2e_light_1", "E2E Light", domain.Light{
		Power: true, Brightness: 200,
	})
	ep, props := endpointWithState(entity)

	// Upsert device.
	conn.send(map[string]any{
		"action":   "device_upsert",
		"clientId": clientID,
		"endpoint": endpointJSON(ep),
		"state":    alexaStatePayload(props),
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("device_upsert failed: %v", resp)
	}
	t.Logf("upserted device: %v", resp["deviceId"])

	// List devices.
	conn.send(map[string]any{
		"action":   "list_devices",
		"clientId": clientID,
	})
	resp = conn.recv()
	if resp["ok"] != true {
		t.Fatalf("list_devices failed: %v", resp)
	}

	devices, _ := resp["devices"].([]any)
	found := false
	for _, d := range devices {
		dm, _ := d.(map[string]any)
		if dm["endpointId"] == ep.EndpointID {
			found = true
			t.Logf("found device in list: endpointId=%v", dm["endpointId"])
			break
		}
	}
	if !found {
		t.Errorf("device %q not found in list_devices response (%d devices)", ep.EndpointID, len(devices))
	}
}

// TestIntegration_StateUpdate updates device state and verifies via list.
func TestIntegration_StateUpdate(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entity := testEntity("switch", "e2e_switch_1", "E2E Switch", domain.Switch{Power: false})
	ep, props := endpointWithState(entity)

	// Upsert first.
	conn.send(map[string]any{
		"action":   "device_upsert",
		"clientId": clientID,
		"endpoint": endpointJSON(ep),
		"state":    alexaStatePayload(props),
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("device_upsert failed: %v", resp)
	}

	// Update state: power on.
	entity.State = domain.Switch{Power: true}
	_, newProps := endpointWithState(entity)
	conn.send(map[string]any{
		"action":   "state_update",
		"clientId": clientID,
		"deviceId": ep.EndpointID,
		"state":    alexaStatePayload(newProps),
	})
	resp = conn.recv()
	if resp["ok"] != true {
		t.Fatalf("state_update failed: %v", resp)
	}
	t.Logf("state_update accepted for %v", ep.EndpointID)

	// Verify via list.
	conn.send(map[string]any{
		"action":   "list_devices",
		"clientId": clientID,
	})
	resp = conn.recv()
	if resp["ok"] != true {
		t.Fatalf("list_devices failed: %v", resp)
	}

	devices, _ := resp["devices"].([]any)
	for _, d := range devices {
		dm, _ := d.(map[string]any)
		if dm["endpointId"] == ep.EndpointID {
			state, _ := dm["state"].(map[string]any)
			stateProps, _ := state["properties"].([]any)
			for _, sp := range stateProps {
				spm, _ := sp.(map[string]any)
				if spm["namespace"] == "Alexa.PowerController" && spm["name"] == "powerState" {
					if spm["value"] != "ON" {
						t.Errorf("expected powerState=ON, got %v", spm["value"])
					} else {
						t.Logf("confirmed powerState=ON after state_update")
					}
					return
				}
			}
			t.Errorf("powerState property not found in device state")
			return
		}
	}
	t.Errorf("device %q not found after state_update", ep.EndpointID)
}

// TestIntegration_DeviceDelete creates and deletes a device.
func TestIntegration_DeviceDelete(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entity := testEntity("switch", "e2e_delete_me", "Delete Me", domain.Switch{Power: false})
	ep, props := endpointWithState(entity)

	// Upsert.
	conn.send(map[string]any{
		"action":   "device_upsert",
		"clientId": clientID,
		"endpoint": endpointJSON(ep),
		"state":    alexaStatePayload(props),
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("device_upsert failed: %v", resp)
	}

	// Delete.
	conn.send(map[string]any{
		"action":    "device_delete",
		"clientId":  clientID,
		"deviceId":  ep.EndpointID,
	})
	resp = conn.recv()
	if resp["ok"] != true {
		t.Fatalf("device_delete failed: %v", resp)
	}
	t.Logf("deleted device: status=%v deviceId=%v", resp["status"], resp["deviceId"])

	// Verify gone from list.
	conn.send(map[string]any{
		"action":   "list_devices",
		"clientId": clientID,
	})
	resp = conn.recv()
	devices, _ := resp["devices"].([]any)
	for _, d := range devices {
		dm, _ := d.(map[string]any)
		if dm["endpointId"] == ep.EndpointID && dm["status"] == "active" {
			t.Errorf("device %q still active after delete", ep.EndpointID)
			return
		}
	}
	t.Logf("confirmed device removed from active list")
}

// TestIntegration_AllEntityEndpoints upserts one device per entity type
// and verifies each appears in the device list with correct capabilities.
func TestIntegration_AllEntityEndpoints(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entities := []domain.Entity{
		testEntity("light", "e2e_light", "E2E Light", domain.Light{Power: true, Brightness: 200, ColorMode: "hs", HS: []float64{120, 80}}),
		testEntity("switch", "e2e_switch", "E2E Switch", domain.Switch{Power: true}),
		testEntity("cover", "e2e_cover", "E2E Cover", domain.Cover{Position: 75}),
		testEntity("lock", "e2e_lock", "E2E Lock", domain.Lock{Locked: true}),
		testEntity("fan", "e2e_fan", "E2E Fan", domain.Fan{Power: true, Percentage: 66}),
		testEntity("sensor", "e2e_sensor", "E2E Sensor", domain.Sensor{Value: "22.5", DeviceClass: "temperature", Unit: "°C"}),
		testEntity("binary_sensor", "e2e_bsensor", "E2E Binary Sensor", domain.BinarySensor{On: true, DeviceClass: "motion"}),
		testEntity("climate", "e2e_climate", "E2E Climate", domain.Climate{HVACMode: "heat", Temperature: 22, HVACModes: []string{"off", "heat", "cool"}, TemperatureUnit: "°C"}),
		testEntity("button", "e2e_button", "E2E Button", domain.Button{}),
		testEntity("number", "e2e_number", "E2E Number", domain.Number{Value: 50, Min: 0, Max: 100, Step: 1}),
		testEntity("select", "e2e_select", "E2E Select", domain.Select{Option: "home", Options: []string{"home", "away"}}),
		testEntity("text", "e2e_text", "E2E Text", domain.Text{Value: "hello", Max: 255}),
		testEntity("alarm", "e2e_alarm", "E2E Alarm", domain.Alarm{AlarmState: "disarmed"}),
		testEntity("camera", "e2e_camera", "E2E Camera", domain.Camera{IsRecording: false}),
		testEntity("valve", "e2e_valve", "E2E Valve", domain.Valve{Position: 50, ReportsPosition: true}),
		testEntity("siren", "e2e_siren", "E2E Siren", domain.Siren{IsOn: false}),
		testEntity("humidifier", "e2e_humid", "E2E Humidifier", domain.Humidifier{IsOn: true, TargetHumidity: 50, MinHumidity: 30, MaxHumidity: 80}),
		testEntity("media_player", "e2e_media", "E2E Media Player", domain.MediaPlayer{State: "playing", VolumeLevel: 0.5, SourceList: []string{"TV", "Radio"}, Source: "TV"}),
		testEntity("remote", "e2e_remote", "E2E Remote", domain.Remote{IsOn: true}),
		testEntity("event", "e2e_event", "E2E Event", domain.Event{EventTypes: []string{"click"}, DeviceClass: "button"}),
	}

	// Upsert all — batch in groups of 9 to stay under the 10/min rate limit.
	const batchSize = 9
	for i, entity := range entities {
		if i > 0 && i%batchSize == 0 {
			t.Logf("rate-limit pause after %d upserts, waiting 61s...", i)
			time.Sleep(61 * time.Second)
		}
		ep, props := endpointWithState(entity)
		conn.send(map[string]any{
			"action":   "device_upsert",
			"clientId": clientID,
			"endpoint": endpointJSON(ep),
			"state":    alexaStatePayload(props),
		})
		resp := conn.recv()
		if resp["ok"] != true {
			t.Fatalf("device_upsert %s failed: %v", entity.Type, resp)
		}
	}
	t.Logf("upserted %d devices", len(entities))

	// List and verify all present.
	conn.send(map[string]any{
		"action":   "list_devices",
		"clientId": clientID,
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("list_devices failed: %v", resp)
	}

	devices, _ := resp["devices"].([]any)
	deviceMap := map[string]map[string]any{}
	for _, d := range devices {
		dm, _ := d.(map[string]any)
		if eid, ok := dm["endpointId"].(string); ok {
			deviceMap[eid] = dm
		}
	}

	for _, entity := range entities {
		ep := translate.ToAlexa(entity)
		t.Run(entity.Type, func(t *testing.T) {
			dm, ok := deviceMap[ep.EndpointID]
			if !ok {
				t.Errorf("device %q not found in list", ep.EndpointID)
				return
			}
			t.Logf("found: endpointId=%s friendlyName=%v", ep.EndpointID, dm["friendlyName"])

			// Verify capabilities are present.
			caps, _ := dm["capabilities"].([]any)
			if len(caps) == 0 {
				t.Errorf("no capabilities for %s", entity.Type)
			}
		})
	}

	// Cleanup: delete all e2e devices (batch to avoid rate limit of 6/min).
	const deleteBatch = 5
	for i, entity := range entities {
		if i > 0 && i%deleteBatch == 0 {
			time.Sleep(61 * time.Second)
		}
		ep := translate.ToAlexa(entity)
		conn.send(map[string]any{
			"action":   "device_delete",
			"clientId": clientID,
			"deviceId": ep.EndpointID,
		})
		resp := conn.recv()
		if resp["ok"] != true {
			t.Logf("cleanup delete %s: %v", ep.EndpointID, resp)
		}
	}
	t.Logf("cleaned up %d devices", len(entities))
}

// TestIntegration_RegisterFailsWithBadSecret verifies auth rejection.
func TestIntegration_RegisterFailsWithBadSecret(t *testing.T) {
	wsURL, relayToken, clientID, _ := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)

	conn.send(map[string]any{
		"action":   "register",
		"clientId": clientID,
		"secret":   "wrong-secret-value",
	})
	resp := conn.recv()
	if resp["ok"] == true {
		t.Fatal("expected register to fail with bad secret, but got ok=true")
	}
	t.Logf("correctly rejected: %v", resp["error"])
}

// TestIntegration_RateLimitInfo exercises rapid state_updates to confirm
// the server handles them gracefully (we don't assert rate-limit hit,
// just that sequential updates work within reason).
func TestIntegration_RapidStateUpdates(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entity := testEntity("light", "e2e_rapid", "Rapid Light", domain.Light{Power: true, Brightness: 100})
	ep, props := endpointWithState(entity)

	// Upsert.
	conn.send(map[string]any{
		"action":   "device_upsert",
		"clientId": clientID,
		"endpoint": endpointJSON(ep),
		"state":    alexaStatePayload(props),
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("upsert failed: %v", resp)
	}

	// Send 10 rapid state updates.
	okCount := 0
	for i := 0; i < 10; i++ {
		entity.State = domain.Light{Power: true, Brightness: i * 25}
		_, newProps := endpointWithState(entity)
		conn.send(map[string]any{
			"action":   "state_update",
			"clientId": clientID,
			"deviceId": ep.EndpointID,
			"state":    alexaStatePayload(newProps),
		})
		resp = conn.recv()
		if resp["ok"] == true {
			okCount++
		}
	}
	t.Logf("%d/%d rapid state_updates accepted", okCount, 10)
	if okCount == 0 {
		t.Error("no state_updates accepted")
	}

	// Cleanup.
	conn.send(map[string]any{
		"action":   "device_delete",
		"clientId": clientID,
		"deviceId": ep.EndpointID,
	})
	conn.recv()
}

// TestIntegration_TranslationFidelity verifies the full pipeline: domain entity →
// ToAlexa → upsert → list → verify the endpoint stored in AWS matches what
// plugin-alexa generated.
func TestIntegration_TranslationFidelity(t *testing.T) {
	wsURL, relayToken, clientID, clientSecret := loadTestEnv(t)
	conn := dial(t, wsURL, relayToken)
	conn.register(clientID, clientSecret)

	entity := testEntity("light", "e2e_fidelity", "Fidelity Light", domain.Light{
		Power:      true,
		Brightness: 180,
		ColorMode:  "hs",
		HS:         []float64{240, 100},
	})
	ep, props := endpointWithState(entity)

	// Upsert.
	conn.send(map[string]any{
		"action":   "device_upsert",
		"clientId": clientID,
		"endpoint": endpointJSON(ep),
		"state":    alexaStatePayload(props),
	})
	resp := conn.recv()
	if resp["ok"] != true {
		t.Fatalf("upsert failed: %v", resp)
	}

	// List and find.
	conn.send(map[string]any{
		"action":   "list_devices",
		"clientId": clientID,
	})
	resp = conn.recv()
	devices, _ := resp["devices"].([]any)
	var found map[string]any
	for _, d := range devices {
		dm, _ := d.(map[string]any)
		if dm["endpointId"] == ep.EndpointID {
			found = dm
			break
		}
	}
	if found == nil {
		t.Fatalf("device not found in list")
	}

	// Verify friendly name survived round-trip.
	if found["friendlyName"] != ep.FriendlyName {
		t.Errorf("friendlyName: want %q, got %v", ep.FriendlyName, found["friendlyName"])
	}

	// Verify capabilities survived round-trip.
	caps, _ := found["capabilities"].([]any)
	capNames := map[string]bool{}
	for _, c := range caps {
		cm, _ := c.(map[string]any)
		if iface, ok := cm["interface"].(string); ok {
			capNames[iface] = true
		}
	}
	expectedCaps := []string{
		"Alexa.PowerController",
		"Alexa.BrightnessController",
		"Alexa.ColorController",
		"Alexa.EndpointHealth",
	}
	for _, want := range expectedCaps {
		if !capNames[want] {
			t.Errorf("missing capability %q in round-tripped endpoint", want)
		}
	}
	t.Logf("capabilities verified: %v", capNames)

	// Verify state round-trip.
	state, _ := found["state"].(map[string]any)
	stateProps, _ := state["properties"].([]any)
	propMap := map[string]any{}
	for _, sp := range stateProps {
		spm, _ := sp.(map[string]any)
		key := fmt.Sprintf("%v.%v", spm["namespace"], spm["name"])
		propMap[key] = spm["value"]
	}

	if propMap["Alexa.PowerController.powerState"] != "ON" {
		t.Errorf("powerState: want ON, got %v", propMap["Alexa.PowerController.powerState"])
	}
	t.Logf("state properties verified: %d properties", len(stateProps))

	// Cleanup.
	conn.send(map[string]any{
		"action":   "device_delete",
		"clientId": clientID,
		"deviceId": ep.EndpointID,
	})
	conn.recv()
}
