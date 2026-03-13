package main

import (
	"context"

	runner "github.com/slidebolt/sdk-runner"
	"github.com/slidebolt/sdk-types"
)

type legacyEventSink interface {
	EmitEvent(evt types.InboundEvent) error
}

type alexaLegacyEventService struct {
	sink legacyEventSink
}

func (a alexaLegacyEventService) PublishEvent(evt types.InboundEvent) error {
	if a.sink == nil {
		return nil
	}
	return a.sink.EmitEvent(evt)
}

type testConfig struct {
	EventSink legacyEventSink
}

func (p *PluginAdapter) OnInitialize(config testConfig, state types.Storage) (types.Manifest, types.Storage) {
	p.storage = state
	manifest, _ := p.Initialize(runner.PluginContext{
		Events: alexaLegacyEventService{sink: config.EventSink},
	})
	return manifest, state
}

func (p *PluginAdapter) OnReady()                            { _ = p.Start(context.Background()) }
func (p *PluginAdapter) WaitReady(ctx context.Context) error { return nil }
func (p *PluginAdapter) OnShutdown()                         { _ = p.Stop() }
func (p *PluginAdapter) OnConfigUpdate(current types.Storage) (types.Storage, error) {
	if current.Meta != "" {
		p.storage.Meta = current.Meta
	}
	return p.storage, nil
}
