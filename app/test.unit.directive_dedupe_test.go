package app

import (
	"testing"
	"time"

	domain "github.com/slidebolt/sb-domain"
)

func TestAlexaDirectiveDedupeSuppressesIdenticalStateCommand(t *testing.T) {
	client := newClient(Config{}, nil, nil)
	now := time.Unix(100, 0)
	client.now = func() time.Time { return now }
	command := domain.LightTurnOn{}

	if !client.shouldRouteDirective("bedroom", command) {
		t.Fatal("first directive must be routed")
	}
	if client.shouldRouteDirective("bedroom", command) {
		t.Fatal("identical directive inside the dedupe window must be suppressed")
	}
	if !client.shouldRouteDirective("bathroom", command) {
		t.Fatal("a different endpoint must not be suppressed")
	}
	now = now.Add(directiveDedupeWindow)
	if !client.shouldRouteDirective("bedroom", command) {
		t.Fatal("directive after the dedupe window must be routed")
	}
}

func TestAlexaDirectiveDedupeKeepsDistinctValuesAndNonIdempotentActions(t *testing.T) {
	client := newClient(Config{}, nil, nil)
	if !client.shouldRouteDirective("bedroom", domain.LightSetBrightness{Brightness: 40}) ||
		!client.shouldRouteDirective("bedroom", domain.LightSetBrightness{Brightness: 80}) {
		t.Fatal("distinct desired values must both be routed")
	}
	if !client.shouldRouteDirective("remote", domain.RemoteSendCommand{Command: "volume_up"}) ||
		!client.shouldRouteDirective("remote", domain.RemoteSendCommand{Command: "volume_up"}) {
		t.Fatal("repeatable actions must never be deduplicated")
	}
}
