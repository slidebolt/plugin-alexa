package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	contract "github.com/slidebolt/sb-contract"
	domain "github.com/slidebolt/sb-domain"
	messenger "github.com/slidebolt/sb-messenger-sdk"
	translate "github.com/slidebolt/plugin-alexa/internal/translate"
	storage "github.com/slidebolt/sb-storage-sdk"
)

const PluginID = "plugin-alexa"

// changeReporter receives entity state changes that should be broadcast to Alexa.
type changeReporter interface {
	BroadcastChangeReport(domain.Entity)
	UpsertDevice(domain.Entity)
}

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
	client *alexaClient
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

	// Connect to the AWS relay as a persistent WebSocket client.
	a.client = newClient(a.cfg, a.store, a.cmds)
	if err := a.client.Start(); err != nil {
		return nil, fmt.Errorf("start alexa client: %w", err)
	}

	// Watch for PluginAlexa-labeled entity changes and push them to AWS.
	if err := startAlexaWatch(a.msg, a.store, a.client); err != nil {
		return nil, fmt.Errorf("start alexa watch: %w", err)
	}

	log.Printf("plugin-alexa: started, connecting to %s", a.cfg.WSURL)
	return nil, nil
}

// capFingerprint returns a JSON string of the entity's Alexa capabilities,
// used to detect when the capability set changes (as opposed to just state).
func capFingerprint(entity domain.Entity) string {
	caps := translate.ToAlexa(entity).Capabilities
	b, _ := json.Marshal(caps)
	return string(b)
}

// startAlexaWatch sets up a storage.Watch for entities labeled PluginAlexa and
// pushes state updates for each one via the client — including entities
// that already exist in storage before the watch starts.
//
// It maintains a cache of each entity's Alexa capability fingerprint so that
// state-only changes produce a lightweight ChangeReport, while capability
// changes trigger a full UpsertDevice (which flows to AddOrUpdateReport).
func startAlexaWatch(msg messenger.Messenger, store storage.Storage, reporter changeReporter) error {
	alexaQuery := storage.Query{
		Where: []storage.Filter{
			{Field: "labels.PluginAlexa", Op: storage.Exists},
		},
	}

	var capMu sync.Mutex
	capCache := map[string]string{} // entityKey → capability fingerprint

	// handleEntity compares the entity's current capabilities against the
	// cached fingerprint. If capabilities changed (or never seen), it calls
	// UpsertDevice; otherwise it calls BroadcastChangeReport.
	handleEntity := func(_ string, data json.RawMessage) {
		var entity domain.Entity
		if err := json.Unmarshal(data, &entity); err != nil {
			return
		}

		fp := capFingerprint(entity)
		key := entity.Key()

		capMu.Lock()
		prev, exists := capCache[key]
		capCache[key] = fp
		capMu.Unlock()

		if !exists || prev != fp {
			reporter.UpsertDevice(entity)
		} else {
			reporter.BroadcastChangeReport(entity)
		}
	}

	_, err := storage.Watch(msg, alexaQuery, storage.WatchHandlers{
		OnAdd:    handleEntity,
		OnUpdate: handleEntity,
	})
	if err != nil {
		return fmt.Errorf("watch alexa entities: %w", err)
	}

	// Push any entities already labeled PluginAlexa that were in storage
	// before the watch subscription was created.
	entries, err := store.Query(alexaQuery)
	if err != nil {
		return fmt.Errorf("query existing alexa entities: %w", err)
	}
	for _, entry := range entries {
		var entity domain.Entity
		if err := json.Unmarshal(entry.Data, &entity); err != nil {
			continue
		}
		fp := capFingerprint(entity)
		capMu.Lock()
		capCache[entity.Key()] = fp
		capMu.Unlock()
		reporter.UpsertDevice(entity)
	}

	return nil
}

func (a *App) OnShutdown() error {
	if a.client != nil {
		a.client.Stop()
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
