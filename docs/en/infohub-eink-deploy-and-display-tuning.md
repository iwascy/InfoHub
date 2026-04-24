# InfoHub E-Ink (reTerminal E1001) Deployment & Display Tuning

This document ties together the entire `reTerminal E1001 + ESPHome + InfoHub API` deployment process, with focus on:

- The final mainline approach
- Directory structure and available commands
- First USB flash and subsequent OTA update workflow
- Key pitfalls: e-paper white screen, HTTP 401, wrong endpoint URLs
- Verified display parameters and interaction behavior

If you need to rebuild the environment later, start with this document and follow links to the detailed runbooks.

## One-Page Summary

The verified mainline pipeline:

1. `Mac Studio` runs `collect-server`
2. `OrbStack Docker` independently runs `ESPHome Dashboard` (`http://localhost:6052`)
3. Use browser `web.esphome.io` for the first USB flash
4. After successful first flash, switch to business firmware and update via OTA
5. The device directly requests `collect-server`'s `/dashboard/eink/device.json`

This pipeline is verified working — the device currently displays the business panel normally.

## Final Architecture

Current deployed architecture:

```text
Mac Studio
├── collect-server
│   ├── HTML dashboard: /dashboard/eink
│   ├── debug JSON:     /dashboard/eink.json
│   └── device JSON:    /dashboard/eink/device.json
├── OrbStack Docker
│   └── ESPHome Dashboard (http://localhost:6052)
├── web.esphome.io
│   └── First-time USB flash of factory firmware
└── reTerminal E1001
    ├── Stage 1: First-flash diagnostic firmware
    └── Stage 2: InfoHub API panel firmware
```

Known host-side addresses:

- `collect-server`: `http://10.30.5.172:8080`
- `ESPHome Dashboard`: `http://localhost:6052`

## Key Repository Files

Current mainline files:

- `Makefile`
- `deploy/esphome/docker/compose.yaml`
- `deploy/esphome/secrets.example.yaml`
- `deploy/esphome/reterminal_e1001_first_flash_alt.yaml`
- `deploy/esphome/reterminal_e1001_infohub_api.yaml`
- `docs/en/infohub-eink-esphome-docker-mac.md`
- `docs/en/infohub-eink-first-flash-runbook.md`
- `docs/en/infohub-eink-direct-api-panel.md`
- `docs/mockups/reterminal-e1001-ui-v7.svg`

The two most critical firmware configurations:

- Stage 1 first flash: `deploy/esphome/reterminal_e1001_first_flash_alt.yaml`
- Stage 2 business firmware: `deploy/esphome/reterminal_e1001_infohub_api.yaml`

## Directory Structure

```text
collect-server/
├── Makefile
├── deploy/
│   └── esphome/
│       ├── docker/
│       │   ├── compose.yaml
│       │   └── .env.example
│       ├── secrets.example.yaml
│       ├── reterminal_e1001_first_flash_alt.yaml    # First flash (recommended)
│       ├── reterminal_e1001_infohub_api.yaml        # Business firmware
│       └── reterminal_e1001_partial_refresh_probe.yaml
└── docs/
    ├── en/
    │   ├── infohub-eink-esphome-docker-mac.md
    │   ├── infohub-eink-first-flash-runbook.md
    │   └── infohub-eink-direct-api-panel.md
    └── mockups/
        └── reterminal-e1001-ui-v7.svg
```

## Full Deployment Flow

### 1. Run ESPHome in Standalone Docker

Compilation, YAML management, and firmware downloads all go through the host's `ESPHome Dashboard` (`http://localhost:6052`). The first flash requires browser Web Serial.

### 2. Prepare Secrets and Environment Variables

From the repository root:

```bash
cp deploy/esphome/secrets.example.yaml deploy/esphome/secrets.yaml
cp deploy/esphome/docker/.env.example deploy/esphome/docker/.env
```

Then fill in `deploy/esphome/secrets.yaml` according to your setup.

Field sources:

| Field | Source |
| --- | --- |
| `wifi_ssid` | The `2.4GHz Wi-Fi` name for the device |
| `wifi_password` | Corresponding Wi-Fi password |
| `wifi_fallback_password` | Custom password for the device's fallback AP |
| `esphome_api_encryption_key` | Generate locally with `openssl rand -base64 32` |
| `esphome_ota_password` | Generate locally with `openssl rand -hex 16` |
| `infohub_eink_device_url` | `collect-server`'s device endpoint URL, format below |

Key generation commands:

```bash
openssl rand -base64 32
openssl rand -hex 16
```

Stage 2 device endpoint URL format:

```text
http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300
```

If using a public domain, it should also point to the `device.json` endpoint, not the HTML dashboard:

```text
https://summary.cccy.fun/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300
```

### 3. Start ESPHome Dashboard on OrbStack

In this environment, running `docker compose` directly often results in:

```text
Cannot connect to the Docker daemon at unix:///var/run/docker.sock
```

This is because the actual runtime is `OrbStack`, not the default Docker context.

Prefix all commands with:

```bash
DOCKER_CONTEXT=orbstack
```

Common commands:

```bash
make DOCKER_CONTEXT=orbstack esphome-config
make DOCKER_CONTEXT=orbstack esphome-pull
make DOCKER_CONTEXT=orbstack esphome-up
make DOCKER_CONTEXT=orbstack esphome-logs
make DOCKER_CONTEXT=orbstack esphome-ps
```

After starting, open:

```text
http://localhost:6052
```

### 4. PlatformIO / ESP-IDF Compilation Issues on macOS Shared Directories

During the process, a `PlatformIO` `framework-espidf` installation failure was encountered, with typical errors like:

- `No such file or directory`
- `shutil.Error`
- Packages located in `/config/.esphome/platformio/...`

The root cause is not YAML syntax — it's `ESP-IDF`'s massive file copying being unstable on macOS shared directories.

Stable solution:

- `/config` maps to the repository's `deploy/esphome`
- `PlatformIO` cache uses Docker volume: `/cache`
- Build artifacts use Docker volume: `/build`
- CLI compilation reuses the running `infohub-esphome` container

Recommended commands:

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

### 5. Stage 1: Get the Screen Working

Do not start with business firmware for the first flash — use the minimal first-flash config:

```text
deploy/esphome/reterminal_e1001_first_flash_alt.yaml
```

Compile first:

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
```

You can also install directly from the `ESPHome Dashboard` and manually download the `factory` firmware.

### 6. First USB Flash with `web.esphome.io`

The reliable first-flash approach:

1. Get the `factory` firmware from the `ESPHome Dashboard`
2. Open [ESPHome Web](https://web.esphome.io/)
3. Click `Connect`
4. Select the serial device
5. Click `Install`
6. Select the `factory` firmware you downloaded

You do not need to click `Prepare for first use` first.

As long as you have the `factory` firmware, go directly to manual install.

### 7. White Screen Root Cause and Fix

After flashing with the standard `7.50inv2` configuration, the behavior was:

- Firmware flashed successfully
- Device boots
- But the e-paper screen is completely white

The confirmed root cause: this `reTerminal E1001` unit's display initialization parameters differ from the typical official examples.

The stable parameters for this device:

```yaml
model: 7.50inv2alt
reset_duration: 2ms
```

Therefore, the first flash must use:

```text
deploy/esphome/reterminal_e1001_first_flash_alt.yaml
```

After this flash, the screen displays `ALT PROFILE`, confirming the white screen issue is resolved.

### 8. Is Stage 1 Firmware Flickering Normal?

After flashing the first-flash diagnostic firmware, the device will flash periodically — this is normal.

The reason is that the Stage 1 firmware configures:

- `update_interval`
- Periodic redraw of test content

Its purpose is to confirm the screen can reliably refresh, not to show the final business display.

### 9. Stage 2: Switch to Business Firmware

After confirming Stage 1 works, switch to:

```text
deploy/esphome/reterminal_e1001_infohub_api.yaml
```

Compile command:

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

Then update the device via OTA.

This firmware's core logic:

- Device directly fetches `infohub_eink_device_url`
- Parses the returned JSON body on device
- If payload is identical to the previous one, screen does not refresh
- Only triggers display update when content or error state changes

## Pitfalls from Business Firmware Integration

### 1. "Data parse failed, check device.json payload"

The common root cause is not corrupted JSON, but a wrong URL.

Wrong:

```text
https://summary.cccy.fun/dashboard/eink?token=...&refresh=600
```

This is the HTML page, not the device endpoint.

Correct:

```text
https://summary.cccy.fun/dashboard/eink/device.json?token=...&refresh=300
```

The device must point to `device.json`.

### 2. `HTTP 401`

This indicates the device can reach the endpoint, but authentication failed.

The fix path in this case was:

1. Confirm the dashboard authentication method on the server
2. Update code and deploy
3. Update `infohub_eink_device_url` in `deploy/esphome/secrets.yaml`
4. Re-flash or OTA update to restore normal operation

This also shows:

- The same token may not work for both the HTML page and the device endpoint
- Even if paths look similar, always verify against the server's current auth logic

### 3. Compilation Errors Beyond "Syntax Issues"

During Stage 2, several configuration issues were encountered beyond API problems:

- Fallback AP name too long
- Duplicate characters in `font.glyphs`
- Using `json::parse_json(...)` without a root-level `json:` component
- UI tweaks breaking lambda content, causing C++ compilation failures

These issues have been fixed in the current `deploy/esphome/reterminal_e1001_infohub_api.yaml`. The current version compiles and deploys successfully.

## Current Device Interaction Behavior

Current button and control behavior in the business firmware:

| Button | Current Behavior |
| --- | --- |
| `GPIO3` | Triggers a manual `fetch_dashboard_payload` |
| `Force Sync` button | Triggers a manual `fetch_dashboard_payload` |
| `GPIO4` | Currently unbound |
| `GPIO5` | Currently unbound |

Note:

- `Force Sync` and `GPIO3` mean "manually fetch latest payload"
- If the fetched body is identical to the previous one, the screen will not force a redraw
- "Pressing the button but not seeing a screen redraw" is by design, not a button malfunction

## E-Ink Display Optimization

The business firmware underwent a round of layout optimization:

- Token counts unified to `M (million)` as display unit
- Removed "request count" and "startup count"
- Removed account names from quota cards
- `Sub2API` multi-account displayed as "combined statistics"
- Overall larger fonts to reduce CJK aliasing on small text

Current visual mockup: `docs/mockups/reterminal-e1001-ui-v7.svg`

## Quick Troubleshooting Reference

| Symptom | Cause | Resolution |
| --- | --- | --- |
| `Cannot connect to the Docker daemon ... docker.sock` | Using `OrbStack` but Docker context not set | Add `DOCKER_CONTEXT=orbstack` to all commands |
| White screen after Stage 1 flash | `7.50inv2` init params incompatible with this display batch | Flash with `reterminal_e1001_first_flash_alt.yaml` instead |
| Stage 1 periodic flickering | Diagnostic firmware is designed to periodically refresh | Normal behavior |
| "Data parse failed" message | Pointing to the HTML page, not `device.json` | Change to `/dashboard/eink/device.json` |
| `HTTP 401` displayed | Token or device endpoint auth mismatch | Check server auth and update `secrets.yaml` |
| `No such file or directory` during PlatformIO install | macOS shared directory file copy instability | Use Docker volumes for cache and build directories |
| Button press without visible redraw | Payload unchanged, designed not to refresh | Expected behavior, not a button issue |

## Current Stable Configuration

- Mainline route: `Mac + OrbStack Docker + Web Serial + OTA`
- First flash uses `7.50inv2alt + reset_duration: 2ms`
- Business firmware switched to `7.50inV2p + full_update_every: 15 + reset_duration: 2ms` (partial refresh verified)
- Business firmware requests `/dashboard/eink/device.json`
- `Force Sync` / `GPIO3` triggers a manual fetch, not an unconditional full-screen redraw

## Related Documents

- Detailed Docker setup: `docs/en/infohub-eink-esphome-docker-mac.md`
- Detailed first-flash runbook: `docs/en/infohub-eink-first-flash-runbook.md`
- Detailed direct API panel guide: `docs/en/infohub-eink-direct-api-panel.md`
