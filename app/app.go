package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	contract "github.com/slidebolt/sb-contract"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const PluginID = "plugin-alexa"

// App is the importable runtime for the plugin-alexa binary.
// Keep production behavior here so tests can exercise it without importing cmd/.
type App struct {
	msg    messenger.Messenger
	store  storage.Storage
	cmds   *messenger.Commands
	subs   []messenger.Subscription
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	srv    *alexaServer
}

func New() *App {
	return &App{}
}

func (a *App) Hello() contract.HelloResponse {
	return contract.HelloResponse{
		ID:              PluginID,
		Kind:            contract.KindPlugin,
		ContractVersion: contract.ContractVersion,
		DependsOn:       []string{"messenger", "storage"},
	}
}

func (a *App) OnStart(deps map[string]json.RawMessage) (json.RawMessage, error) {
	a.cfg = loadConfig()
	a.ctx, a.cancel = context.WithCancel(context.Background())

	msg, err := messenger.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect messenger: %w", err)
	}
	a.msg = msg

	store, err := storage.Connect(deps)
	if err != nil {
		return nil, fmt.Errorf("connect storage: %w", err)
	}
	a.store = store

	a.cmds = messenger.NewCommands(msg, domain.LookupCommand)

	// Subscribe to commands for entities owned by this plugin.
	sub, err := a.cmds.Receive(PluginID+".>", a.handleCommand)
	if err != nil {
		return nil, fmt.Errorf("subscribe commands: %w", err)
	}
	a.subs = append(a.subs, sub)

	// Subscribe to all state changes so we can push Alexa change reports for
	// any entity labeled PluginAlexa, regardless of owning plugin.
	stateSub, err := a.msg.Subscribe("state.changed.>", a.handleStateChanged)
	if err != nil {
		return nil, fmt.Errorf("subscribe state changes: %w", err)
	}
	a.subs = append(a.subs, stateSub)

	a.srv = newServer(a.cfg, a.store, a.cmds)
	port, err := a.srv.Start()
	if err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}

	log.Printf("plugin-alexa: started on port %d, advertising via mDNS", port)
	return nil, nil
}

// handleStateChanged is called for every state.changed.{key} event on the bus.
// If the entity has the PluginAlexa label, push a change report to the relay.
func (a *App) handleStateChanged(m *messenger.Message) {
	var entity domain.Entity
	if err := json.Unmarshal(m.Data, &entity); err != nil {
		return
	}
	if _, ok := entity.Labels["PluginAlexa"]; !ok {
		return
	}
	if a.srv != nil {
		a.srv.BroadcastChangeReport(entity)
	}
}

func (a *App) OnShutdown() error {
	if a.srv != nil {
		a.srv.Stop()
	}
	for _, sub := range a.subs {
		sub.Unsubscribe()
	}
	if a.store != nil {
		a.store.Close()
	}
	if a.msg != nil {
		a.msg.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	return nil
}
