package device

import (
	sdk "github.com/slidebolt/plugin-sdk"
)

// alexaFanOutScript fans out commands to members and tracks local state.
// Identical in structure to the group fan-out in plugin-automation.
const alexaFanOutScript = `
	local function setDeviceState(ctx, status)
		ctx.UpdateState(status)
	end

	local function publishEntityState(ctx, status, raw, payload)
		local statePayload = { power = (status == "on") }
		statePayload.brightness = (raw and raw.brightness) or 100
		if raw and raw.color_rgb then
			statePayload.r = raw.color_rgb.r
			statePayload.g = raw.color_rgb.g
			statePayload.b = raw.color_rgb.b
		end
		if raw and raw.color_temperature then
			statePayload.kelvin = raw.color_temperature
		end
		if payload and payload.scene then
			statePayload.scene = payload.scene
		end
		ctx.UpdateProperties(statePayload)
	end

	function onCommand(cmd, payload, ctx)
		local raw = ctx.GetDeviceRaw() or {}
		local members = raw.members or {}
		local p = payload or {}
		ctx.Log("Alexa device " .. ctx.ID .. " fanning out command: " .. cmd)
		for _, id in ipairs(members) do
			local msg = {}
			for k, v in pairs(p) do msg[k] = v end
			msg.command = cmd
			ctx.Publish("entity." .. id .. ".command", msg)
			ctx.Publish("device." .. id .. ".command", msg)
		end

		if payload then
			if payload.r or payload.g or payload.b then
				raw.color_rgb = { r = payload.r or 0, g = payload.g or 0, b = payload.b or 0 }
			end
			if payload.kelvin then
				raw.color_temperature = payload.kelvin
			end
			if payload.level then
				raw.brightness = payload.level
			end
		end
		ctx.UpdateRaw(raw)

		local c = string.lower(cmd or "")
		local status = "last_cmd_" .. (cmd or "")
		if c == "turnoff" or c == "toggleoff" then
			status = "off"
		elseif c == "turnon" or c == "toggleon" or c == "setbrightness" or c == "setrgb" or c == "settemperature" or c == "setscene" then
			status = "on"
		end
		setDeviceState(ctx, "active")
		publishEntityState(ctx, status, raw, payload)
	end
`

// CreateAlexaDevice creates a virtual device managed by plugin-alexa.
// entityType and capabilities declare what kind of Alexa device this is.
// members is the list of real entity UUIDs to fan commands out to.
func CreateAlexaDevice(b sdk.Bundle, name, sid string, entityType sdk.EntityType, capabilities []string, members []string) (sdk.Device, error) {
	dev, err := b.CreateDevice()
	if err != nil {
		return nil, err
	}
	dev.UpdateMetadata(name, sdk.SourceID(sid))
	dev.UpdateRaw(map[string]interface{}{
		"type":    "alexa_device",
		"members": members,
	})

	ent, err := dev.CreateEntityEx(entityType, capabilities)
	if err != nil {
		return nil, err
	}
	ent.UpdateScript(alexaFanOutScript)

	return dev, nil
}
