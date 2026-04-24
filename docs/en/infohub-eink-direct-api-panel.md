# InfoHub E-Ink Direct API Panel

> This document covers the Stage 2 business panel.
> For the initial USB first flash, start with the [reTerminal E1001 First Flash Runbook](./infohub-eink-first-flash-runbook.md) to confirm the device, display, and OTA pipeline are working, then come back here.
> The recommended compilation/management entry point is the [ESPHome Docker on macOS](./infohub-eink-esphome-docker-mac.md) guide.

This is Stage 2 of the recommended path: `reTerminal E1001 + ESPHome` directly requests the device endpoint provided by this project.

Core data endpoints:

- HTML dashboard: `/dashboard/eink?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- Debug JSON: `/dashboard/eink.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- Device direct JSON: `/dashboard/eink/device.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=300`

## Why Direct API

This approach better fits the current requirements:

1. No screenshots needed, no additional rendering service required
2. The panel consumes the project API directly — the device side does not depend on browser rendering
3. ESPHome only triggers an e-paper refresh when the payload changes, avoiding unnecessary repeated screen updates
4. The device-side layout is a high-fidelity replica of the current HTML dashboard, maintaining the same visual structure with three overview cards, two tables, and a right-side alert column

## Repository Files

- Device endpoint: `GET /dashboard/eink/device.json`
- ESPHome template: [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)

## 1. Verify Project Endpoints

After starting the project, verify three entry points:

```bash
curl "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
curl "http://10.30.5.172:8080/dashboard/eink.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
curl "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

The device endpoint returns a compact structure optimized for ESPHome parsing, containing:

- `updated_at`
- `claude`
- `sub2api`
- `total`
- `claude_rows`
- `sub2api_rows`
- `alerts`
- `reset_hints`

## 2. ESPHome Device Direct Connection

After completing Stage 1 first flash, use [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) as the device YAML.

Key points of this template:

- `http_request.get` directly fetches `device.json`
- `capture_response: true` to get the full JSON body on device
- `max_response_buffer_size: 16384` to avoid the 1KB default buffer being too small
- `update_interval: never` — the display does not refresh on a fixed cycle
- If the HTTP response body is identical to the previous one, `component.update` is not triggered
- `GPIO3` is reserved as a physical manual refresh button
- `GPIO4` also serves as the wake-up key for nighttime deep sleep
- A `Force Sync` button is also exposed
- Three runtime states are supported: "plugged high-frequency / battery power-saving / battery nighttime silent"

ESPHome `secrets.yaml` requires at least these values:

```yaml
wifi_ssid: "YOUR_WIFI"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "YOUR_FALLBACK_PASSWORD"
esphome_api_encryption_key: "YOUR_ESPHOME_API_KEY"
esphome_ota_password: "YOUR_OTA_PASSWORD"
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

You can also copy directly from [deploy/esphome/secrets.example.yaml](../../deploy/esphome/secrets.example.yaml) and fill in real values.

### Power-Saving Polling Strategy

The current API template includes a conservative power-saving strategy:

- Plugged mode: poll every `2min`
- Battery mode: poll every `5min`
- Battery nighttime silent: from `22:00` to `10:00` the next day, no API requests — the device enters deep sleep and auto-wakes at `10:00`
- E-paper refresh retains the "only refresh when payload changes" logic, so plugged mode polls more frequently but does not cause repeated screen refreshes for identical content
- If battery level drops below threshold, the top status bar displays a `Low Battery` indicator

The template also exposes these entities:

- `Battery Voltage`
- `Battery Level`
- `Power Profile`

### How Power Mode Is Determined

This version uses Seeed's officially documented battery measurement method:

- `GPIO21` enables battery voltage measurement
- `GPIO1` reads the battery voltage

Two voltage thresholds provide approximate classification:

- `>= 4.15V` treated as plugged / high-frequency mode
- `<= 4.05V` treated as battery / power-saving mode
- `<= 20%` triggers the top `Low Battery` indicator

This is a "practical but not perfectly precise" default. The reason is that the publicly available documentation clearly supports battery voltage sampling, but not a verified USB/VBUS power detection pin for this device.

If in practice you observe:

- The device still shows "plugged" briefly after unplugging at full charge
- Or switching is slow when charging with voltage below threshold

You can adjust the substitutions at the top of [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml):

- `plugged_voltage_threshold`
- `battery_voltage_threshold`
- `low_battery_level_threshold`

### Verified Configuration Notes

These two issues were encountered and confirmed on 2026-04-22 in a real ESPHome environment:

- The fallback AP `ssid` must not exceed 32 characters, so do not use `"${friendly_name} Fallback"` style long names — the current template uses `InfoHub Fallback`
- `font.glyphs` in ESPHome 2026.4.1 strictly validates duplicate characters — duplicate spaces, newlines, or CJK characters cause `esphome config` to fail immediately; the glyph set in the current template has been deduplicated

The current API template passes `esphome config` validation and compiles/deploys successfully.

### Display Parameters

The display parameters for this `reTerminal E1001` have been verified:

- First flash uses `7.50inv2alt + reset_duration: 2ms` to confirm the screen lights up
- Business firmware has been switched to `7.50inV2p + full_update_every: 15 + reset_duration: 2ms`, supporting hardware-level partial refresh
- Standard `7.50inv2` causes a white screen on this device — do not use

## 3. Partial Refresh Status

The current business firmware achieves partial refresh on two levels:

1. **Logic level**: screen only refreshes when the API payload changes
2. **Physical display level**: uses `7.50inV2p` for hardware partial refresh, with `full_update_every: 15` performing a full refresh every 15 partial refreshes to prevent ghosting accumulation

## 4. Recommended Deployment Order

1. Complete the [reTerminal E1001 First Flash Runbook](./infohub-eink-first-flash-runbook.md) — confirm minimal firmware is USB-flashed and the screen shows content
2. Start and confirm `collect-server`'s `device.json` is accessible
3. Switch the device YAML to the API direct version
4. Update the device via OTA, not repeated USB flashing
5. Verify that the screen only refreshes when JSON content changes
6. If using the power-saving template, observe the `Power Profile` / `Battery Voltage` / `Battery Level` entities to confirm plugged/battery switching matches the actual voltage behavior of this device
7. If `esphome config` fails, first check Wi-Fi fallback name length, `font.glyphs` for duplicate characters, and whether the root-level `json:` component is missing

To further verify hardware partial refresh, do not switch display models directly on the business panel — use the standalone probe firmware first:
[reTerminal E1001 Partial Refresh Probe](./infohub-eink-partial-refresh-probe.md)

## 5. Further Power Saving

The current version already includes "no requests at night + nighttime deep sleep" in the template. To further extend battery life:

- Switch daytime battery mode to "wake on schedule, request once, then deep sleep again" — this saves significantly more power than maintaining a Wi-Fi connection
- If a reliable USB/VBUS detection pin is identified later, replace the current voltage-based approximation with true external power detection for faster switching
- If the backend collection doesn't change at minute-level frequency, extend `battery_poll_interval` from `5min` to `15min`, `30min`, or longer
- If only screen content preservation is needed at night without network connectivity, consider disabling Wi-Fi before entering silent mode or entering deep sleep earlier

## References

- Seeed official E1001 + ESPHome wiring and `waveshare_epaper` example:
  [reTerminal E Series with ESPHome](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- ESPHome official `waveshare_epaper` component docs:
  [Waveshare E-Paper Display](https://esphome.io/components/display/waveshare_epaper.html)
- ESPHome official `http_request` component docs:
  [HTTP Request Component](https://esphome.io/components/http_request.html)
