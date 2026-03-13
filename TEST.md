# Package Level Requirements

Tests for the plugin-alexa project should verify:

- **Directive Forwarding**: Handling Alexa directives and forwarding them to target entities.
- **Proxy Device Support**: Mapping "proxy" devices to other plugins' entities.
- **Capability Mapping**: Mapping internal entity types (sensor, lock, cover) to Alexa capabilities.
- **SDK Alignment**: Using RawStore for persistence without local shadow registries.
- **Proactive Reporting**: Forwarding state changes to the Alexa relay.
- **Error Status**: Updating `sync_status` and reporting errors when forwarding fails.
- **Availability Entity**: Exposing a `binary_sensor` for proxy device availability.
- **Common Requirements**: Registration, entity snapshots, and health reporting.
