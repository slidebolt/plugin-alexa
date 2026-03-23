package app

import "testing"

func TestNewHelloManifest(t *testing.T) {
	h := New().Hello()
	if h.ID != PluginID {
		t.Fatalf("id: got %q want %q", h.ID, PluginID)
	}
	if len(h.DependsOn) != 2 || h.DependsOn[0] != "messenger" || h.DependsOn[1] != "storage" {
		t.Fatalf("dependsOn: got %v want [messenger storage]", h.DependsOn)
	}
}
