# plugin-alexa Instructions

plugin-alexa bridges the SlideBolt entity system with Amazon Alexa via the
Alexa Smart Home Skill API v3. It exposes entities labeled `PluginAlexa` to
Alexa through a relay service, handling all translation between SlideBolt
domain types and Alexa interfaces (capabilities, directives, state reports).

## Architecture

- **Label-based discovery**: entities with `labels.PluginAlexa` are exposed
- **WebSocket server**: relay connects via WS, receives discovery + change reports
- **2-way binding**: state changes → ChangeReports; directives → domain commands
- **Full translation**: plugin builds complete Alexa API payloads; relay is dumb

## Directory layout

```
app/           importable production runtime (app.go, server.go, commands.go, config.go)
cmd/           thin binary wrapper (main.go) + BDD/integration tests
internal/      translate layer (Decode/Encode + ToAlexa/FromAlexa)
```

## Test layers

- **Unit tests** (`go test ./...`): domain logic, translate, Alexa mapping
- **BDD tests** (`go test -tags bdd ./...`): per-entity behavioral contract
- **Integration tests**: require real relay service

## Key conventions

- `app/` must remain importable — no `func main()` in app/
- `cmd/plugin-alexa/main.go` is ≤ 12 lines: `runtime.Run(app.New())`
- All 20 entity types supported with Alexa capability mapping
- Brightness scaling: SB 0-254 ↔ Alexa 0-100
- Color: Alexa HSV ↔ SB HS/RGB with conversion
