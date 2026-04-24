# ESPHome Docker on macOS

This setup runs `ESPHome` independently in Docker Desktop on a `Mac Studio` for firmware compilation and device management.

Key advantages:

- `ESPHome Dashboard` runs independently, making issues easier to isolate
- Devices pull the project API directly, with no external service dependency

## Scope

This setup is suitable for:

- Stably compiling ESPHome on Mac
- Running the local ESPHome Dashboard
- Generating `factory` firmware
- First-time browser/USB flashing of the `reTerminal E1001`
- Subsequent OTA updates to the business panel

What this setup does NOT do:

- Direct USB flashing from within the Docker container

The reason is that `Docker Desktop on macOS` is not well suited for passing host USB devices directly into the ESPHome container. The recommended approach is:

1. Use Docker's ESPHome to compile firmware
2. Manually download `factory` firmware from the Dashboard
3. Use browser Web Serial or other host-side USB tools to flash the device

This is consistent with ESPHome's official recommended usage on macOS with Docker.

## Directory Structure

The repository is organized as follows:

```text
collect-server/
├── Makefile
├── deploy/
│   └── esphome/
│       ├── docker/
│       │   ├── compose.yaml
│       │   └── .env.example
│       ├── secrets.example.yaml
│       ├── reterminal_e1001_first_flash_alt.yaml
│       ├── reterminal_e1001_infohub_api.yaml
│       └── reterminal_e1001_partial_refresh_probe.yaml
└── docs/
    ├── en/
    │   ├── infohub-eink-first-flash-runbook.md
    │   └── infohub-eink-direct-api-panel.md
    └── zh/
        └── ...
```

Key files:

- [compose.yaml](../../deploy/esphome/docker/compose.yaml) starts the local `ESPHome Dashboard`
- [secrets.example.yaml](../../deploy/esphome/secrets.example.yaml) is the device-side secrets template
- [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml) first-flash config (`7.50inv2alt`, verified working)
- [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) business panel (`7.50inV2p`, supports partial refresh)
- [reterminal_e1001_partial_refresh_probe.yaml](../../deploy/esphome/reterminal_e1001_partial_refresh_probe.yaml) partial refresh probe (verified passing)

Note:

- `/config` maps to the repository's `deploy/esphome`
- `PlatformIO` package cache uses the container volume `/cache`
- Build artifacts use the container volume `/build`

This avoids the file copy failures that occur when `ESP-IDF`'s many files go through OrbStack/macOS shared directories.

## One-Time Setup

From the repository root:

```bash
cp deploy/esphome/secrets.example.yaml deploy/esphome/secrets.yaml
cp deploy/esphome/docker/.env.example deploy/esphome/docker/.env
```

Then edit:

- `deploy/esphome/secrets.yaml`
- `deploy/esphome/docker/.env`

Minimum required values:

```yaml
# deploy/esphome/secrets.yaml
wifi_ssid: "Your 2.4G Wi-Fi SSID"
wifi_password: "Your Wi-Fi password"
wifi_fallback_password: "Set a separate password"
esphome_api_encryption_key: "Generate with: openssl rand -base64 32"
esphome_ota_password: "Generate with: openssl rand -hex 16"
```

If you don't have the key/password yet:

```bash
openssl rand -base64 32
openssl rand -hex 16
```

## Available Commands

All commands below can be run from the repository root.

If you are using `OrbStack`, prefix commands with:

```bash
DOCKER_CONTEXT=orbstack
```

For example:

```bash
make DOCKER_CONTEXT=orbstack esphome-up
```

### 1. Validate compose

```bash
make DOCKER_CONTEXT=orbstack esphome-config
```

### 2. Pull images

```bash
make DOCKER_CONTEXT=orbstack esphome-pull
```

### 3. Start ESPHome Dashboard

```bash
make DOCKER_CONTEXT=orbstack esphome-up
```

After starting, the local address defaults to:

```text
http://localhost:6052
```

### 4. View logs

```bash
make DOCKER_CONTEXT=orbstack esphome-logs
```

### 5. Check container status

```bash
make DOCKER_CONTEXT=orbstack esphome-ps
```

### 6. Stop the Dashboard

```bash
make DOCKER_CONTEXT=orbstack esphome-down
```

### 7. Recreate containers after compose changes

```bash
make DOCKER_CONTEXT=orbstack esphome-recreate
```

## Two Usage Paths

### Path A: Recommended — Dashboard-Based First Flash

This is the most reliable main path.

1. Run:

```bash
make DOCKER_CONTEXT=orbstack esphome-up
```

2. Open:

```text
http://localhost:6052
```

3. In the Dashboard, import or edit:

- For this device, use [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml)

4. Choose to manually download the `factory` firmware
5. Use browser Web Serial or a host-side USB tool for the first flash
6. After the screen shows `ALT PROFILE`, switch to:

- [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)

### Path B: CLI-Based Compilation Verification

If you want to rule out YAML/font/dependency issues first, run compilation only:

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
```

Stage 2 business panel compilation:

```bash
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

CLI compilation runs directly inside the already-running `infohub-esphome` container, explicitly reusing the image's `/entrypoint.sh`. This avoids `PlatformIO` package state corruption from multiple containers writing to the same cache concurrently. It also ensures the `/cache` and `/build` volume configurations apply during CLI compilation.

## Recommended Operation Sequence

### Stage 1: Get the Device Running

1. Prepare `deploy/esphome/secrets.yaml`
2. Run `make DOCKER_CONTEXT=orbstack esphome-up`
3. Open `http://localhost:6052`
4. Use [reterminal_e1001_first_flash_alt.yaml](../../deploy/esphome/reterminal_e1001_first_flash_alt.yaml) to generate the `factory` firmware
5. Flash the device via browser/USB for the first time
6. Confirm the screen shows `ALT PROFILE`

### Stage 2: Switch to the Business Panel

1. Add to `deploy/esphome/secrets.yaml`:

```yaml
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

2. Switch the device to [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)
3. Update via OTA
4. Verify that the screen only refreshes when JSON content changes

## FAQ

### 1. Why does the compose file set `ESPHOME_DASHBOARD_USE_PING=true`?

The ESPHome official documentation specifically recommends enabling this option for Docker on Mac, making the Dashboard's device online check more reliable.

### 2. Why are extra `/cache` and `/build` volumes mounted?

The ESPHome official image's `entrypoint.sh` detects when `/cache` and `/build` are mounted and:

- Places `PlatformIO` platforms/packages/cache in `/cache`
- Places build output in `/build`

This is important for OrbStack/macOS because `ESP-IDF` installation involves massive file copying. Moving this high-frequency I/O off the shared directory `/config` significantly improves stability.

### 3. Why not flash USB directly from the Docker container?

Because on macOS, Docker Desktop runs with Linux virtualization. Reliably passing USB through to the ESPHome container is not a strength of this approach.

The more stable combination is:

- Docker handles compilation and Dashboard
- Browser/host handles the first USB flash
- After the device is online, switch to OTA

### 4. How to configure proxy?

If font or dependency downloads are slow, edit the `.env` file copied from:

- [deploy/esphome/docker/.env.example](../../deploy/esphome/docker/.env.example)

Set:

```dotenv
HTTP_PROXY=http://10.30.5.172:7897
HTTPS_PROXY=http://10.30.5.172:7897
NO_PROXY=localhost,127.0.0.1,10.30.5.0/24
```

The container will inherit these environment variables on startup.

## Related Files

- Compose file: [compose.yaml](../../deploy/esphome/docker/compose.yaml)
- Docker env example: [.env.example](../../deploy/esphome/docker/.env.example)
- First flash runbook: [infohub-eink-first-flash-runbook.md](./infohub-eink-first-flash-runbook.md)
- Direct API panel: [infohub-eink-direct-api-panel.md](./infohub-eink-direct-api-panel.md)

## References

- ESPHome official CLI and Docker guide:
  [Getting Started with the Command Line and Docker](https://esphome.io/guides/getting_started_command_line/)
