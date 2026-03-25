// Unit tests for plugin-alexa.
//
// Test layer philosophy:
//   Unit tests (this file): pure domain logic, cross-entity behavior,
//     and custom entity type registration. Things that don't express
//     well as BDD scenarios or that test infrastructure capabilities
//     across multiple entity types simultaneously.
//
//   BDD tests (features/*.feature, -tags bdd): per-entity behavioral
//     contract. One feature file per entity type. These are the
//     source of truth for what a plugin promises to support.
//
// Run:
//   go test ./...              - unit tests only
//   go test -tags bdd ./...    - unit tests + BDD scenarios

package app

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
	testkit "github.com/slidebolt/sb-testkit"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
)

// reportCall records which method was called and with which entity.
type reportCall struct {
	method string // "change" or "upsert"
	entity domain.Entity
}

// mockReporter records BroadcastChangeReport and UpsertDevice calls for testing.
type mockReporter struct {
	mu    sync.Mutex
	seen  []domain.Entity
	calls []reportCall
}

func (r *mockReporter) BroadcastChangeReport(e domain.Entity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, e)
	r.calls = append(r.calls, reportCall{method: "change", entity: e})
}

func (r *mockReporter) UpsertDevice(e domain.Entity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, e)
	r.calls = append(r.calls, reportCall{method: "upsert", entity: e})
}

func (r *mockReporter) wait(t *testing.T, count int) []domain.Entity {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.seen)
		r.mu.Unlock()
		if n >= count {
			r.mu.Lock()
			out := append([]domain.Entity(nil), r.seen...)
			r.mu.Unlock()
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: want %d reports, got %d", count, len(r.seen))
	return nil
}

func (r *mockReporter) waitCalls(t *testing.T, count int) []reportCall {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.calls)
		r.mu.Unlock()
		if n >= count {
			r.mu.Lock()
			out := append([]reportCall(nil), r.calls...)
			r.mu.Unlock()
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	n := len(r.calls)
	r.mu.Unlock()
	t.Fatalf("timed out: want %d calls, got %d", count, n)
	return nil
}

func (r *mockReporter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

// ==========================================================================
// Custom entity type — defined by this plugin, NOT in sb-domain.
// Proves that plugins can register and use their own types end-to-end.
// ==========================================================================

type Sprinkler struct {
	Zone     int     `json:"zone"`
	Active   bool    `json:"active"`
	Moisture float64 `json:"moisture"`
	Schedule string  `json:"schedule,omitempty"`
}

type SprinklerActivate struct {
	Zone     int `json:"zone"`
	Duration int `json:"duration"`
}

func (SprinklerActivate) ActionName() string { return "sprinkler_activate" }

type SprinklerDeactivate struct {
	Zone int `json:"zone"`
}

func (SprinklerDeactivate) ActionName() string { return "sprinkler_deactivate" }

func init() {
	domain.Register("sprinkler", Sprinkler{})
	domain.RegisterCommand("sprinkler_activate", SprinklerActivate{})
	domain.RegisterCommand("sprinkler_deactivate", SprinklerDeactivate{})
}

// --- Test helpers ---

func env(t *testing.T) (*testkit.TestEnv, storage.Storage, *messenger.Commands) {
	t.Helper()
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	cmds := messenger.NewCommands(e.Messenger(), domain.LookupCommand)
	return e, e.Storage(), cmds
}

func saveEntity(t *testing.T, store storage.Storage, plugin, device, id, typ, name string, state any) domain.Entity {
	t.Helper()
	e := domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type: typ, Name: name, State: state,
	}
	if err := store.Save(e); err != nil {
		t.Fatalf("save %s: %v", id, err)
	}
	return e
}

func getEntity(t *testing.T, store storage.Storage, plugin, device, id string) domain.Entity {
	t.Helper()
	raw, err := store.Get(domain.EntityKey{Plugin: plugin, DeviceID: device, ID: id})
	if err != nil {
		t.Fatalf("get %s.%s.%s: %v", plugin, device, id, err)
	}
	var entity domain.Entity
	if err := json.Unmarshal(raw, &entity); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return entity
}

func queryByType(t *testing.T, store storage.Storage, typ string) []storage.Entry {
	t.Helper()
	entries, err := store.Query(storage.Query{
		Where: []storage.Filter{{Field: "type", Op: storage.Eq, Value: typ}},
	})
	if err != nil {
		t.Fatalf("query type=%s: %v", typ, err)
	}
	return entries
}

func sendAndReceive(t *testing.T, cmds *messenger.Commands, entity domain.Entity, cmd any, pattern string) any {
	t.Helper()
	done := make(chan any, 1)
	cmds.Receive(pattern, func(addr messenger.Address, c any) {
		done <- c
	})
	if err := cmds.Send(entity, cmd.(messenger.Action)); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-done:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
		return nil
	}
}

// ==========================================================================
// Internal storage: plugin-private data, invisible to query/search
// ==========================================================================

func TestInternal_WriteReadDelete(t *testing.T) {
	_, store, _ := env(t)
	key := domain.EntityKey{Plugin: "test", DeviceID: "dev1", ID: "light001"}
	payload := json.RawMessage(`{"commandTopic":"zigbee2mqtt/living_room/set","brightnessScale":254}`)

	if err := store.WriteFile(storage.Internal, key, payload); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := store.ReadFile(storage.Internal, key)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("ReadFile: got %s, want %s", got, payload)
	}

	if err := store.DeleteFile(storage.Internal, key); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := store.ReadFile(storage.Internal, key); err == nil {
		t.Fatal("expected ReadFile to fail after DeleteFile")
	}
}

func TestInternal_NotVisibleInQuery(t *testing.T) {
	_, store, _ := env(t)
	key := domain.EntityKey{Plugin: "test", DeviceID: "dev1", ID: "light001"}

	// Save a normal entity and an internal payload for the same key.
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light", domain.Light{Power: true})
	store.WriteFile(storage.Internal, key, json.RawMessage(`{"commandTopic":"zigbee2mqtt/foo/set"}`))

	// Query must return exactly 1 entity — the state entity, not the internal data.
	entries := queryByType(t, store, "light")
	if len(entries) != 1 {
		t.Fatalf("query: got %d results, want 1", len(entries))
	}
}

func TestInternal_NotVisibleInSearch(t *testing.T) {
	_, store, _ := env(t)
	key := domain.EntityKey{Plugin: "test", DeviceID: "dev1", ID: "light001"}

	saveEntity(t, store, "test", "dev1", "light001", "light", "Light", domain.Light{Power: true})
	store.WriteFile(storage.Internal, key, json.RawMessage(`{"commandTopic":"zigbee2mqtt/foo/set"}`))

	entries, err := store.Search("test.>")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("search: got %d results, want 1", len(entries))
	}
	if entries[0].Key != "test.dev1.light001" {
		t.Errorf("search result key: got %q, want test.dev1.light001", entries[0].Key)
	}
}

// ==========================================================================
// Cross-cutting: multi-plugin isolation, query all powered-on
// ==========================================================================

func TestCrossCutting_MultiPluginIsolation(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "esphome", "dev1", "light001", "light", "ESP Light", domain.Light{Power: true})
	saveEntity(t, store, "zigbee", "dev1", "light001", "light", "Zigbee Light", domain.Light{Power: true})

	entries, _ := store.Query(storage.Query{
		Pattern: "esphome.>",
		Where:   []storage.Filter{{Field: "type", Op: storage.Eq, Value: "light"}},
	})
	if len(entries) != 1 {
		t.Fatalf("esphome lights: got %d, want 1", len(entries))
	}
}

func TestCrossCutting_QueryAllPoweredOn(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "test", "dev1", "light001", "light", "On Light", domain.Light{Power: true})
	saveEntity(t, store, "test", "dev1", "light002", "light", "Off Light", domain.Light{Power: false})
	saveEntity(t, store, "test", "dev1", "switch01", "switch", "On Switch", domain.Switch{Power: true})
	saveEntity(t, store, "test", "dev1", "fan001", "fan", "Off Fan", domain.Fan{Power: false})

	entries, _ := store.Query(storage.Query{
		Where: []storage.Filter{{Field: "state.power", Op: storage.Eq, Value: true}},
	})
	if len(entries) != 2 {
		t.Fatalf("powered on: got %d, want 2", len(entries))
	}
}

// ==========================================================================
// Custom entity: Sprinkler — full end-to-end BDD
// ==========================================================================

func TestCustom_Sprinkler_SaveGetHydrate(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front Lawn",
		Sprinkler{Zone: 1, Active: true, Moisture: 42.5, Schedule: "6am"})

	got := getEntity(t, store, "irrigation", "yard1", "zone-front")
	s, ok := got.State.(Sprinkler)
	if !ok {
		t.Fatalf("state type: got %T, want Sprinkler", got.State)
	}
	if s.Zone != 1 || !s.Active || s.Moisture != 42.5 || s.Schedule != "6am" {
		t.Errorf("state: %+v", s)
	}
}

func TestCustom_Sprinkler_QueryByType(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front", Sprinkler{Zone: 1})
	saveEntity(t, store, "irrigation", "yard1", "zone-back", "sprinkler", "Back", Sprinkler{Zone: 2})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light", domain.Light{Power: true})

	entries := queryByType(t, store, "sprinkler")
	if len(entries) != 2 {
		t.Fatalf("sprinklers: got %d, want 2", len(entries))
	}
}

func TestCustom_Sprinkler_QueryByMoisture(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Moisture: 30})
	saveEntity(t, store, "irrigation", "yard1", "zone-back", "sprinkler", "Back",
		Sprinkler{Zone: 2, Moisture: 70})
	saveEntity(t, store, "irrigation", "yard1", "zone-side", "sprinkler", "Side",
		Sprinkler{Zone: 3, Moisture: 55})

	entries, err := store.Query(storage.Query{
		Where: []storage.Filter{
			{Field: "type", Op: storage.Eq, Value: "sprinkler"},
			{Field: "state.moisture", Op: storage.Lt, Value: float64(60)},
		},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("dry zones: got %d, want 2", len(entries))
	}
}

func TestCustom_Sprinkler_QueryByActive(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Active: true})
	saveEntity(t, store, "irrigation", "yard1", "zone-back", "sprinkler", "Back",
		Sprinkler{Zone: 2, Active: false})

	entries, err := store.Query(storage.Query{
		Where: []storage.Filter{
			{Field: "type", Op: storage.Eq, Value: "sprinkler"},
			{Field: "state.active", Op: storage.Eq, Value: true},
		},
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("active sprinklers: got %d, want 1", len(entries))
	}
}

func TestCustom_Sprinkler_Activate(t *testing.T) {
	_, _, cmds := env(t)
	entity := domain.Entity{ID: "zone-front", Plugin: "irrigation", DeviceID: "yard1", Type: "sprinkler"}
	got := sendAndReceive(t, cmds, entity, SprinklerActivate{Zone: 1, Duration: 300}, "irrigation.>")
	cmd, ok := got.(SprinklerActivate)
	if !ok {
		t.Fatalf("type: got %T, want SprinklerActivate", got)
	}
	if cmd.Zone != 1 || cmd.Duration != 300 {
		t.Errorf("command: %+v", cmd)
	}
}

func TestCustom_Sprinkler_Deactivate(t *testing.T) {
	_, _, cmds := env(t)
	entity := domain.Entity{ID: "zone-front", Plugin: "irrigation", DeviceID: "yard1", Type: "sprinkler"}
	got := sendAndReceive(t, cmds, entity, SprinklerDeactivate{Zone: 1}, "irrigation.>")
	cmd, ok := got.(SprinklerDeactivate)
	if !ok {
		t.Fatalf("type: got %T, want SprinklerDeactivate", got)
	}
	if cmd.Zone != 1 {
		t.Errorf("zone: got %d, want 1", cmd.Zone)
	}
}

// ==========================================================================
// Mixed entities: custom + built-in — proves isolation and coexistence
// ==========================================================================

func TestMixed_QueryEachTypeInIsolation(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Sprinkler",
		Sprinkler{Zone: 1, Active: true, Moisture: 40})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light",
		domain.Light{Power: true, Brightness: 200})
	saveEntity(t, store, "test", "dev1", "switch01", "switch", "Switch",
		domain.Switch{Power: true})
	saveEntity(t, store, "test", "dev1", "temp01", "sensor", "Temp",
		domain.Sensor{Value: 22.5, Unit: "°C"})
	saveEntity(t, store, "test", "dev1", "hvac01", "climate", "AC",
		domain.Climate{HVACMode: "cool", Temperature: 21})

	tests := []struct {
		typ   string
		count int
	}{
		{"sprinkler", 1},
		{"light", 1},
		{"switch", 1},
		{"sensor", 1},
		{"climate", 1},
	}
	for _, tc := range tests {
		entries := queryByType(t, store, tc.typ)
		if len(entries) != tc.count {
			t.Errorf("%s: got %d, want %d", tc.typ, len(entries), tc.count)
		}
	}
}

func TestMixed_CustomAndBuiltinBoolField(t *testing.T) {
	_, store, _ := env(t)
	// Sprinkler has state.active=true, Light has state.power=true
	// These are DIFFERENT field names — querying one must not match the other.
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Active: true})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light",
		domain.Light{Power: true})
	saveEntity(t, store, "test", "dev1", "switch01", "switch", "Switch",
		domain.Switch{Power: true})

	// Query state.active=true should only match sprinklers
	active, _ := store.Query(storage.Query{
		Where: []storage.Filter{{Field: "state.active", Op: storage.Eq, Value: true}},
	})
	if len(active) != 1 {
		t.Fatalf("state.active=true: got %d, want 1 (only sprinkler)", len(active))
	}

	// Query state.power=true should only match light + switch
	powered, _ := store.Query(storage.Query{
		Where: []storage.Filter{{Field: "state.power", Op: storage.Eq, Value: true}},
	})
	if len(powered) != 2 {
		t.Fatalf("state.power=true: got %d, want 2 (light+switch)", len(powered))
	}
}

func TestMixed_NumericCrossType(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Moisture: 80})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light",
		domain.Light{Brightness: 80})
	saveEntity(t, store, "test", "dev1", "temp01", "sensor", "Temp",
		domain.Sensor{Value: 80})

	// Query moisture > 50 — only sprinkler has this field
	entries, _ := store.Query(storage.Query{
		Where: []storage.Filter{
			{Field: "state.moisture", Op: storage.Gt, Value: float64(50)},
		},
	})
	if len(entries) != 1 {
		t.Fatalf("state.moisture>50: got %d, want 1", len(entries))
	}

	// Hydrate the result and verify it's a Sprinkler
	var entity domain.Entity
	json.Unmarshal(entries[0].Data, &entity)
	if _, ok := entity.State.(Sprinkler); !ok {
		t.Fatalf("hydrated type: got %T, want Sprinkler", entity.State)
	}
}

func TestMixed_PatternIsolatesPlugin(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Sprinkler",
		Sprinkler{Zone: 1, Active: true})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light",
		domain.Light{Power: true})

	// Pattern restricts to irrigation plugin only
	entries, _ := store.Query(storage.Query{
		Pattern: "irrigation.>",
	})
	if len(entries) != 1 {
		t.Fatalf("irrigation pattern: got %d, want 1", len(entries))
	}

	// No type filter, just pattern — verify only irrigation entities come back
	var entity domain.Entity
	json.Unmarshal(entries[0].Data, &entity)
	if entity.Plugin != "irrigation" {
		t.Errorf("plugin: got %q, want irrigation", entity.Plugin)
	}
}

func TestMixed_DeleteCustomDoesNotAffectBuiltin(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Sprinkler",
		Sprinkler{Zone: 1})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Light",
		domain.Light{Power: true})

	// Delete the sprinkler
	store.Delete(domain.EntityKey{Plugin: "irrigation", DeviceID: "yard1", ID: "zone-front"})

	// Sprinkler should be gone
	entries := queryByType(t, store, "sprinkler")
	if len(entries) != 0 {
		t.Fatalf("sprinklers after delete: got %d, want 0", len(entries))
	}

	// Light should still be there
	entries = queryByType(t, store, "light")
	if len(entries) != 1 {
		t.Fatalf("lights after delete: got %d, want 1", len(entries))
	}
}

func TestMixed_OverwriteCustomReflectsInQuery(t *testing.T) {
	_, store, _ := env(t)
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Moisture: 30})

	// Overwrite with new moisture value
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front",
		Sprinkler{Zone: 1, Moisture: 90})

	// Query should reflect the updated value
	entries, _ := store.Query(storage.Query{
		Where: []storage.Filter{
			{Field: "type", Op: storage.Eq, Value: "sprinkler"},
			{Field: "state.moisture", Op: storage.Gt, Value: float64(80)},
		},
	})
	if len(entries) != 1 {
		t.Fatalf("after overwrite: got %d, want 1", len(entries))
	}
}

func TestMixed_FullLifecycle_SaveQueryCommandHydrate(t *testing.T) {
	_, store, cmds := env(t)

	// Save a mix of custom and built-in entities
	saveEntity(t, store, "irrigation", "yard1", "zone-front", "sprinkler", "Front Lawn",
		Sprinkler{Zone: 1, Active: false, Moisture: 35})
	saveEntity(t, store, "test", "dev1", "light001", "light", "Kitchen",
		domain.Light{Power: false, Brightness: 0})

	// Query for all sprinklers — should find 1
	sprinklers := queryByType(t, store, "sprinkler")
	if len(sprinklers) != 1 {
		t.Fatalf("sprinklers: got %d, want 1", len(sprinklers))
	}

	// Hydrate the sprinkler from query result
	var sprinklerEntity domain.Entity
	if err := json.Unmarshal(sprinklers[0].Data, &sprinklerEntity); err != nil {
		t.Fatalf("unmarshal sprinkler: %v", err)
	}
	s, ok := sprinklerEntity.State.(Sprinkler)
	if !ok {
		t.Fatalf("hydrated sprinkler: got %T, want Sprinkler", sprinklerEntity.State)
	}
	if s.Moisture != 35 {
		t.Errorf("moisture: got %f, want 35", s.Moisture)
	}

	// Send custom command to the sprinkler
	got := sendAndReceive(t, cmds, sprinklerEntity,
		SprinklerActivate{Zone: 1, Duration: 600}, "irrigation.>")
	activate, ok := got.(SprinklerActivate)
	if !ok {
		t.Fatalf("command type: got %T, want SprinklerActivate", got)
	}
	if activate.Duration != 600 {
		t.Errorf("duration: got %d, want 600", activate.Duration)
	}

	// Send built-in command to the light
	lightEntity := domain.Entity{ID: "light001", Plugin: "test", DeviceID: "dev1", Type: "light"}
	gotLight := sendAndReceive(t, cmds, lightEntity,
		domain.LightSetBrightness{Brightness: 254}, "test.>")
	setBr, ok := gotLight.(domain.LightSetBrightness)
	if !ok {
		t.Fatalf("command type: got %T, want LightSetBrightness", gotLight)
	}
	if setBr.Brightness != 254 {
		t.Errorf("brightness: %v", setBr.Brightness)
	}
}

// ==========================================================================
// Alexa change-report watching
//
// The plugin must broadcast a change report for any entity labeled PluginAlexa
// when its state changes. Critically, it must also broadcast for entities that
// ALREADY HAVE the label when the plugin starts — not just future changes.
// ==========================================================================

func alexaLabeledEntity(plugin, device, id, name string, state any) domain.Entity {
	return domain.Entity{
		ID: id, Plugin: plugin, DeviceID: device,
		Type:  "light",
		Name:  name,
		State: state,
	}
}

// saveAlexaEntity saves an entity and sets the PluginAlexa label via SetProfile.
// Labels must go through SetProfile (sidecar) — store.Save strips them.
func saveAlexaEntity(t *testing.T, store storage.Storage, plugin, device, id, name string, state any) domain.Entity {
	t.Helper()
	ent := alexaLabeledEntity(plugin, device, id, name, state)
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}
	key := domain.EntityKey{Plugin: plugin, DeviceID: device, ID: id}
	profile := json.RawMessage(`{"labels":{"PluginAlexa":["default"]}}`)
	if err := store.SetProfile(key, profile); err != nil {
		t.Fatal(err)
	}
	// Re-save so state.changed fires with the merged label data.
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}
	return ent
}

// TestAlexaWatch_FiresForPreExistingLabeledEntity verifies that an entity with
// the PluginAlexa label that already exists in storage before the watch starts
// receives a change report immediately — without waiting for a state change.
//
// This fails with the current implementation because the raw state.changed.>
// subscription only sees future events; it never scans existing storage.
func TestAlexaWatch_FiresForPreExistingLabeledEntity(t *testing.T) {
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	msg := e.Messenger()
	store := e.Storage()

	// Entity is in storage BEFORE the watch starts.
	saveAlexaEntity(t, store, "test", "dev1", "light1", "Kitchen", domain.Light{Power: true})

	reporter := &mockReporter{}
	if err := startAlexaWatch(msg, store, reporter); err != nil {
		t.Fatal(err)
	}

	// Expect a change report without any state.changed event being published.
	got := reporter.wait(t, 1)
	if got[0].ID != "light1" {
		t.Errorf("reported entity: got %q, want light1", got[0].ID)
	}
}

// TestAlexaWatch_IgnoresUnlabeledEntities verifies that state changes for
// entities without the PluginAlexa label do NOT produce change reports.
func TestAlexaWatch_IgnoresUnlabeledEntities(t *testing.T) {
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	msg := e.Messenger()
	store := e.Storage()

	reporter := &mockReporter{}
	if err := startAlexaWatch(msg, store, reporter); err != nil {
		t.Fatal(err)
	}

	// Save an entity WITHOUT the PluginAlexa label — should not trigger a report.
	unlabeled := domain.Entity{
		ID: "switch1", Plugin: "test", DeviceID: "dev1",
		Type: "switch", Name: "Switch",
		State: domain.Switch{Power: true},
	}
	if err := store.Save(unlabeled); err != nil {
		t.Fatal(err)
	}

	// Give any spurious reports a moment to arrive.
	time.Sleep(100 * time.Millisecond)
	if n := reporter.count(); n != 0 {
		t.Errorf("unlabeled entity: got %d reports, want 0", n)
	}
}

// TestAlexaWatch_FiresOnLabeledEntityStateChange verifies that a state change
// to a PluginAlexa-labeled entity after the watch is started produces a report.
func TestAlexaWatch_FiresOnLabeledEntityStateChange(t *testing.T) {
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	msg := e.Messenger()
	store := e.Storage()

	// Set up PluginAlexa label via SetProfile before watch starts.
	key := domain.EntityKey{Plugin: "test", DeviceID: "dev1", ID: "light2"}
	profile := json.RawMessage(`{"labels":{"PluginAlexa":["default"]}}`)
	if err := store.SetProfile(key, profile); err != nil {
		t.Fatal(err)
	}

	reporter := &mockReporter{}
	if err := startAlexaWatch(msg, store, reporter); err != nil {
		t.Fatal(err)
	}

	// State change AFTER watch starts — entity already has PluginAlexa label.
	ent := alexaLabeledEntity("test", "dev1", "light2", "Living Room", domain.Light{Power: true, Brightness: 200})
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}

	got := reporter.wait(t, 1)
	if got[0].ID != "light2" {
		t.Errorf("reported entity: got %q, want light2", got[0].ID)
	}
}

// ==========================================================================
// Alexa capability-change detection
//
// When an entity's Alexa capabilities change (e.g. gains color temperature),
// the watch must call UpsertDevice — not just BroadcastChangeReport — so the
// updated endpoint definition reaches DynamoDB and triggers an
// AddOrUpdateReport to Alexa.
// ==========================================================================

// TestAlexaWatch_UpsertsOnCapabilityChange verifies that when an entity's
// capabilities change (e.g. a light gains ColorTemperatureController), the
// watch calls UpsertDevice instead of BroadcastChangeReport.
func TestAlexaWatch_UpsertsOnCapabilityChange(t *testing.T) {
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	msg := e.Messenger()
	store := e.Storage()

	// Save a brightness-only light with the PluginAlexa label.
	saveAlexaEntity(t, store, "test", "dev1", "light3", "Movie Room",
		domain.Light{Power: true, Brightness: 200})

	reporter := &mockReporter{}
	if err := startAlexaWatch(msg, store, reporter); err != nil {
		t.Fatal(err)
	}

	// Wait for the initial UpsertDevice from the pre-existing entity scan.
	reporter.waitCalls(t, 1)

	// Now update the entity: add color temperature mode. This changes
	// the Alexa capabilities (adds ColorTemperatureController).
	ent := domain.Entity{
		ID: "light3", Plugin: "test", DeviceID: "dev1",
		Type: "light", Name: "Movie Room",
		State: domain.Light{Power: true, Brightness: 200, ColorMode: "color_temp", Temperature: 300},
	}
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}

	// Wait for the second call triggered by the state change.
	calls := reporter.waitCalls(t, 2)

	// The second call must be an upsert (capability change), not a change report.
	second := calls[1]
	if second.method != "upsert" {
		t.Errorf("capability change: got method %q, want \"upsert\"", second.method)
	}
	if second.entity.ID != "light3" {
		t.Errorf("entity ID: got %q, want light3", second.entity.ID)
	}
}

// TestAlexaWatch_StateOnlyChangeStillBroadcasts verifies that when only the
// state changes (no capability change), the watch calls BroadcastChangeReport.
func TestAlexaWatch_StateOnlyChangeStillBroadcasts(t *testing.T) {
	e := testkit.NewTestEnv(t)
	e.Start("messenger")
	e.Start("storage")
	msg := e.Messenger()
	store := e.Storage()

	// Save a light with the PluginAlexa label.
	saveAlexaEntity(t, store, "test", "dev1", "light4", "Hallway",
		domain.Light{Power: true, Brightness: 100})

	reporter := &mockReporter{}
	if err := startAlexaWatch(msg, store, reporter); err != nil {
		t.Fatal(err)
	}

	// Wait for initial upsert from pre-existing entity scan.
	reporter.waitCalls(t, 1)

	// Update brightness only — no capability change.
	ent := domain.Entity{
		ID: "light4", Plugin: "test", DeviceID: "dev1",
		Type: "light", Name: "Hallway",
		State: domain.Light{Power: true, Brightness: 200},
	}
	if err := store.Save(ent); err != nil {
		t.Fatal(err)
	}

	calls := reporter.waitCalls(t, 2)

	// The second call should be a change report, not an upsert.
	second := calls[1]
	if second.method != "change" {
		t.Errorf("state-only change: got method %q, want \"change\"", second.method)
	}
}
