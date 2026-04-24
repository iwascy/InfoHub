# reTerminal E1001 First Flash Runbook

This runbook covers Stage 1 of the current mainline approach: get the `reTerminal E1001` stably flashed as an OTA-capable ESPHome device, then switch to the InfoHub business panel.

The recommended approach is the standalone Mac Docker setup:
[ESPHome Docker on macOS](./infohub-eink-esphome-docker-mac.md)

There are only 4 goals:

1. Obtain a stable, compilable `factory` firmware
2. Successfully flash the device via USB for the first time
3. See the e-paper display working
4. Have the device appear in the ESPHome Dashboard over Wi-Fi

Once these 4 items are confirmed, switch to the API direct panel: [infohub-eink-direct-api-panel.md](./infohub-eink-direct-api-panel.md)

## Confirmed Hardware Findings

As of 2026-04-22, first-flash testing on this `reTerminal E1001` unit yielded clear conclusions:

- The standard `7.50inv2` configuration results in a blank white screen after flashing
- Switching to [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml) resolved the display issue
- All subsequent firmware for this device should use the same display initialization parameters:
  `model: 7.50inv2alt` + `reset_duration: 2ms`

The `alt` configuration mentioned in this runbook is the verified working configuration for this device, not just a fallback option.

## Why a Minimal First Flash

The first flash is split into two stages:

- Stage 1 only verifies hardware, USB, compilation, Wi-Fi, and ESPHome online status
- Stage 2 loads [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) for API fetching and page layout

This greatly reduces the troubleshooting surface.

## 0. Pre-Flash Checklist

Before starting, confirm:

- The device has a stable power supply — do not flash with low battery
- Using `2.4GHz Wi-Fi`
- Mac has USB serial drivers installed

If the device is sleeping or the screen is off, press the wake button on the back.

## 1. Copy Stage 1 Files

This repository includes the first-flash files:

- Recommended first-flash YAML (verified working on this device):
  [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
- Secrets template:
  [secrets.example.yaml](../../deploy/esphome/secrets.example.yaml)

> The repository also contains `reterminal_e1001_first_flash.yaml` (standard `7.50inv2`), but it causes a white screen on this device — do not use it.

Copy `secrets.example.yaml` to `secrets.yaml` in the ESPHome device directory, and fill in at least these 5 fields:

```yaml
wifi_ssid: "YOUR_2.4G_WIFI_SSID"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "CHANGE_ME_123"
esphome_api_encryption_key: "REPLACE_WITH_32_BYTE_BASE64_KEY"
esphome_ota_password: "REPLACE_WITH_OTA_PASSWORD"
```

`infohub_eink_device_url` is not needed at this stage.

If you don't have an API encryption key and OTA password yet, generate them on the host:

```bash
openssl rand -base64 32
openssl rand -hex 16
```

The first command is typically used for `esphome_api_encryption_key`, and the second for `esphome_ota_password`.

## 2. Import the Minimal YAML in ESPHome UI

Recommended steps:

1. Follow [ESPHome Docker on macOS](./infohub-eink-esphome-docker-mac.md) to start the local `ESPHome Dashboard`
2. Open `http://localhost:6052`
3. Create or edit a device
4. For this device, paste [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
5. Save
6. Trigger the install via Dashboard, and choose the workflow that generates a downloadable `factory` firmware

At this stage, you only need to obtain the initial `factory` firmware.

## 3. First USB Flash

For the very first flash, use the official browser/USB route:

1. Connect the `reTerminal E1001` via USB
2. Open the ESPHome Web install page or the install entry exported from ESPHome UI
3. Select the serial port
4. Write the `factory` firmware obtained earlier

If the serial port is not visible in the browser:

- Confirm the USB cable supports data transfer
- Confirm the device is awake
- Confirm serial drivers are installed on the Mac

## 4. How to Verify Stage 1 Success

After a successful first flash, the screen should display a simple diagnostic page with:

- `ALT PROFILE`
- `7.50inv2alt + reset_duration 2ms`
- If Wi-Fi is connected: `SSID` and `IP`
- If Wi-Fi is not yet connected: `WiFi pending` and the fallback AP name

Additionally:

- `GPIO3` is reserved as a manual redraw button
- The device should appear online in the ESPHome Dashboard

If this step succeeds, it confirms:

- USB first-flash pipeline works end-to-end
- Display driver pinout is correct
- This device works with `waveshare_epaper + 7.50inv2alt + reset_duration: 2ms`
- The device is ready for subsequent OTA updates

## 5. Switch to Stage 2 After Stage 1 Success

After confirming Stage 1 is working, switch the device YAML to:

- [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)

Then add to `secrets.yaml`:

```yaml
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

Stage 2 verifies these business objectives:

1. The device can fetch `/dashboard/eink/device.json`
2. The e-paper only refreshes when JSON content changes
3. Subsequent updates go through OTA, not repeated USB flashing

If you want to verify whether this display supports hardware-level partial refresh, do not experiment directly on the business YAML — flash the standalone probe first:
[reTerminal E1001 Partial Refresh Probe](./infohub-eink-partial-refresh-probe.md)

## 6. Troubleshooting

### A. Cannot obtain `factory` firmware from compilation

Check first:

- ESPHome UI error output
- YAML / secrets configuration issues
- Font download or external network access failures

Troubleshoot in the ESPHome Dashboard first.

### B. Firmware downloads but USB write fails

Check first:

- USB cable is data-capable, not charge-only
- Device is awake
- Serial drivers are installed
- Browser has serial port permissions

### C. Firmware flashed but screen stays dark

Check first:

- Unstable power supply
- Device has not fully rebooted
- Display initialization timing instability for this batch

Only investigate with this Stage 1 YAML — do not also change business API settings at the same time.

If the screen remains completely white after flashing, do not suspect Wi-Fi or API issues.

Stage 1 display content does not depend on network. A "white screen" means display initialization parameters are mismatched. Confirm you are using `reterminal_e1001_first_flash_alt.yaml` (`7.50inv2alt + reset_duration: 2ms`), not the standard `7.50inv2`.

### D. Screen works but Wi-Fi does not connect

Check first:

- Using `5GHz` Wi-Fi instead of 2.4GHz
- SSID or password typo
- Weak signal

In this case, check the `WiFi pending` message on screen — do not rush to switch to the business panel.

## 7. Related Files

- Recommended first-flash config: [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
- Business panel config: [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)
- Direct API panel guide: [infohub-eink-direct-api-panel.md](./infohub-eink-direct-api-panel.md)
