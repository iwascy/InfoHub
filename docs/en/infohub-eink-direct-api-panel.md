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
4. The default Codex layout shows daily / calendar-week tokens above quotas and reset times for two selected emails, without alerts. The original DeepSeek + Codex layout is also selectable

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
- `codex`: Codex-only daily usage
- `template`: `trio` (default), `codex` or `deepseek-codex`
- `platforms`: `trio` only — Claude Pro, Codex and Antigravity in that order, each with `name`, `today` (daily tokens), `today_label`, `usage_note` (week-to-date usage) and `rows` (per account / model group `five_hour` / `week` quota, same shape as `codex_accounts` except `five_hour.reset_short` holds only the time of day; `not_applicable` is true when the group has no such window upstream)
- `codex_week`: week-to-date tokens, or `--` when unavailable
- `daily_range` / `week_range`: usage date ranges
- `codex_accounts`: email-allowlisted quotas in configured order; missing accounts retain unknown placeholders, and `reset_short` uses Asia/Shanghai
- `codex_rows` / `codex_focus` / `alerts` / `reset_hints`: full legacy quota and alert data

Legacy fields (`deepseek`, `deepseek_quota`, `total`) remain compatible but are not displayed or used to trigger refreshes in the `trio` / `codex` templates; `trio` refreshes only when `platforms` changes. After compiling and flashing the updated business YAML once, all three layouts can be selected through the response's `template` field.

Set `dashboard.template` to `trio` (default), `codex` or `deepseek-codex` and restart the server to change the shared default. All dashboard endpoints accept a request-local `?template=codex` override; archived HTML and firmware for the older layouts live in `docs/panel-templates/codex/` and `docs/panel-templates/deepseek-codex/`. The `trio` device layout shows up to 3 platforms with 4 rows each. retain the token and refresh parameters. The web table displays the configured `dashboard.accounts` allowlist; the device displays its first two entries. Collector account targets must also include the desired emails (matched against account email, credentials.email, or extra.email). The supplied configs include both emails and retain the legacy Pro 20x target.

Weekly usage uses explicit dates from Monday through today in Asia/Shanghai. User targets count only `/responses` tokens; account targets retain the same scope as daily account usage. Weekly items use `token_usage_product_week` and never enter daily totals. Existing snapshots without weekly items display `--` until the next collection.

## 2. ESPHome Device Direct Connection

After completing Stage 1 first flash, use [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) as the device YAML.

Key points of this template:

- `http_request.get` directly fetches `device.json`
- `capture_response: true` to get the full JSON body on device
- `max_response_buffer_size: 16384` to avoid the 1KB default buffer being too small
- `update_interval: never` — the display does not refresh on a fixed cycle
- Only visible Codex usage, quotas, and status changes trigger refreshes; collection timestamps and DeepSeek changes do not. Quota changes update immediately instead of waiting for a periodic full-payload synchronization
- `GPIO3` is reserved as a physical manual refresh button
- `GPIO4` also serves as the manual wake key for battery deep sleep
- A `Force Sync` button is also exposed
- Pressing `GPIO4` during battery deep sleep forces one synchronization, then returns to the appropriate sleep schedule
- Three runtime states are supported: "plugged high-frequency / battery power-saving / battery nighttime silent"
- Wi-Fi uses `fast_connect: true` and `power_save_mode: HIGH`, with automatic fallback AP startup disabled to reduce battery-mode network power use
- The fixed `5s` post-connect delay is removed, and a `60s` awake watchdog returns the device to deep sleep after Wi-Fi or API failures
- HTTP request timeout is reduced from `20s` to `10s`, so network failures keep the device awake for less time

ESPHome `secrets.yaml` requires at least these values:

```yaml
wifi_ssid: "YOUR_WIFI"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "YOUR_FALLBACK_PASSWORD"
# Optional: pair with manual_ip in the YAML to shorten wake-to-Wi-Fi time
wifi_static_ip: "10.30.5.173"
wifi_gateway: "10.30.5.1"
wifi_subnet: "255.255.255.0"
esphome_api_encryption_key: "YOUR_ESPHOME_API_KEY"
esphome_ota_password: "YOUR_OTA_PASSWORD"
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

You can also copy directly from [deploy/esphome/secrets.example.yaml](../../deploy/esphome/secrets.example.yaml) and fill in real values.

### Power-Saving Polling Strategy

The current API template uses a scheduled wake strategy:

- Plugged mode: poll every `2min`
- Battery mode: wake every `30min`, fetch data, refresh only when needed, then immediately return to deep sleep
- Low-battery mode: automatically extend the wake interval to `2h`
- Battery voltage/level sampling: once per wake; every `2min` while plugged and continuously online
- Power Profile state: update every `5min`
- Battery nighttime silent: from `22:00` to `10:00` the next day, no routine API requests — the device enters deep sleep, auto-wakes at `10:00`, or wakes on `GPIO4` for one manual refresh
- E-paper refresh retains the "only refresh when payload changes" logic, so plugged mode polls more frequently but does not cause repeated screen refreshes for identical content
- Only a compact fingerprint of render-relevant payload fields and the partial-refresh counter are persisted; an unchanged payload does not redraw after a deep-sleep reboot
- `GPIO21` is enabled only for the short battery sampling window and is turned off immediately afterward
- If battery level drops below threshold, the top status bar displays a `Low Battery` indicator

In battery mode, API/OTA is available only during the short wake window, and a successful sync is followed by immediate sleep. Before OTA, connect USB power and confirm the device enters the plugged high-frequency profile. The `60s` value is only a maximum awake fallback for network failures, not a fixed OTA window.

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

You can adjust these substitutions at the top of [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml):

- `plugged_voltage_threshold`
- `battery_voltage_threshold`
- `low_battery_level_threshold`
- `battery_wake_interval_minutes`
- `low_battery_wake_interval_minutes`

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

The current version includes all-day scheduled battery wake and nighttime deep sleep. To extend battery life further:

- If a reliable USB/VBUS detection pin is identified later, replace the current voltage-based approximation with true external power detection for faster switching
- If backend data changes infrequently, change `battery_wake_interval_minutes` from `30` to `60` or longer
- Measure wake duration and deep-sleep current with a power profiler; if sleep current remains high, inspect the onboard regulator and peripheral power rails

## References

- Seeed official E1001 + ESPHome wiring and `waveshare_epaper` example:
  [reTerminal E Series with ESPHome](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- ESPHome official `waveshare_epaper` component docs:
  [Waveshare E-Paper Display](https://esphome.io/components/display/waveshare_epaper.html)
- ESPHome official `http_request` component docs:
  [HTTP Request Component](https://esphome.io/components/http_request.html)
