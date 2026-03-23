package app_test

import (
	"encoding/json"
	"testing"

	alexaapp "github.com/slidebolt/plugin-alexa/app"
	domain "github.com/slidebolt/sb-domain"
	managersdk "github.com/slidebolt/sb-manager-sdk"
)

func TestStorageContract_AlexaLabelMergesFromProfile(t *testing.T) {
	env := managersdk.NewTestEnv(t)
	env.Start("messenger")
	env.Start("storage")
	store := env.Storage()

	entity := domain.Entity{
		ID:       "light1",
		Plugin:   "test-plugin",
		DeviceID: "dev1",
		Type:     "light",
		Name:     "Alexa Light",
		Commands: []string{"light_turn_on", "light_turn_off"},
		State:    domain.Light{Power: true, Brightness: 200},
	}
	if err := store.Save(entity); err != nil {
		t.Fatalf("save entity: %v", err)
	}
	profile, _ := json.Marshal(map[string]any{
		"labels": map[string][]string{"PluginAlexa": {"true"}},
	})
	if err := store.SetProfile(entity, json.RawMessage(profile)); err != nil {
		t.Fatalf("set profile: %v", err)
	}

	raw, err := store.Get(domain.EntityKey{Plugin: "test-plugin", DeviceID: "dev1", ID: "light1"})
	if err != nil {
		t.Fatalf("get merged entity: %v", err)
	}
	var got domain.Entity
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal merged entity: %v", err)
	}
	if got.Labels["PluginAlexa"][0] != "true" {
		t.Fatalf("labels = %v", got.Labels)
	}
	if len(got.Commands) != 2 {
		t.Fatalf("commands = %v", got.Commands)
	}
	if _, ok := got.State.(domain.Light); !ok {
		t.Fatalf("state type = %T, want domain.Light", got.State)
	}
	_ = alexaapp.PluginID
}
