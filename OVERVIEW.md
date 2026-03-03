### `plugin-alexa` repository

#### Project Overview

This repository contains the `plugin-alexa`, which acts as a bridge between the Slidebolt system and Amazon Alexa. It allows users to control their Slidebolt-managed devices using voice commands through an Alexa-enabled device.

#### Architecture

The `plugin-alexa` connects to a custom relay service via a persistent WebSocket connection. This relay service is responsible for the direct communication with the Alexa cloud.

The workflow is as follows:

1.  **Voice Command**: A user issues a voice command to an Alexa device (e.g., "Alexa, turn on the living room light").
2.  **Alexa Directive**: The Alexa cloud processes the command and sends a "directive" to the relay service.
3.  **Relay to Plugin**: The relay service forwards this directive to the `plugin-alexa` over the WebSocket connection.
4.  **Directive to Event**: The plugin receives the directive, parses it, and translates it into a standard Slidebolt event (e.g., a `turn_on` command for a `light` entity).
5.  **Event Bus**: The plugin emits this event onto the Slidebolt event bus.
6.  **Device Control**: The appropriate plugin for the target device (e.g., `plugin-wiz`, `plugin-esphome`) picks up the event and executes the command on the physical device.
7.  **State Reporting**: When a device's state changes within Slidebolt (either through Alexa or other means), this plugin receives the event, translates it into an Alexa `ChangeReport`, and sends it back through the relay to keep Alexa's state in sync.

#### Key Files

| File | Description |
| :--- | :--- |
| `main.go` | The core plugin logic that initializes the connection to the relay service, handles incoming directives, and translates them into Slidebolt events. |
| `relay.go` | A WebSocket client for maintaining the persistent connection to the custom relay service. |
| `alexa.go` | Contains helper functions and data structures for creating Alexa-compliant discovery payloads and state reports. |
| `.env.example` | Specifies the environment variables required to configure the plugin, such as the WebSocket endpoint (`ALEXA_WS_ENDPOINT`) and credentials for the relay service. |

#### Available Commands

This plugin primarily acts as a translator and does not directly control devices. It listens for Alexa directives and converts them into Slidebolt events. It also exposes a special `control` device that can be used to programmatically add or remove which Slidebolt devices are exposed to Alexa.

#### Standalone Discovery Mode

This plugin supports a standalone discovery mode for rapid testing and diagnostics without requiring the full Slidebolt stack (NATS, Gateway, etc.).

To run discovery and output the results to JSON:
```bash
./plugin-alexa -discover
```

**Note**: Ensure any required environment variables (e.g., API keys, URLs) are set before running.
