# TASK: plugin-alexa

## Status: INCOMPLETE — Directive Forwarding Is Broken

### Resolved
- [x] Broken storage persistence — `OnStorageUpdate` now returns `p.storage` with updated `Data`
- [x] Independent NATS connection removed — `OnEvent` used for inbound state events instead
- [x] Alexa→SDK state push implemented in `OnEvent`
- [x] Unsafe type assertions replaced with guarded patterns throughout directive handling
- [x] "control" device and entity now correctly exposed in `OnDevicesList` / `OnEntitiesList`

### Still Broken (Critical)

#### Directive Forwarding is a Dead Function (Critical)
`forwardDirectiveToTarget` builds the translated command payload and then does nothing.
The function body ends with a comment block acknowledging the problem:
> "Since we can't access runner's nc, we might need a workaround..."

The old NATS publish was the only working forward path. It was removed but not replaced.
Every Alexa command (TurnOn, TurnOff, SetBrightness, SetColor, etc.) is silently dropped.
The core inbound command feature of this plugin is completely non-functional.

- [ ] The runner SDK needs to expose a way for plugins to dispatch commands to other plugins,
  OR the plugin architecture needs to define a sanctioned cross-plugin command pattern
- [ ] Once that mechanism exists, implement it in `forwardDirectiveToTarget` to replace
  the removed NATS publish

### Residual Housekeeping
- [ ] `go.mod` still lists `nats-io/nats.go` as an indirect dependency — run `go mod tidy`