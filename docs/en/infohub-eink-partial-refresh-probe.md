# reTerminal E1001 Partial Refresh Probe

This document corresponds to a standalone experimental firmware:
[reterminal_e1001_partial_refresh_probe.yaml](../../deploy/esphome/reterminal_e1001_partial_refresh_probe.yaml)

The original goal was not to replace the current business panel, but to confirm with minimal risk whether this `reTerminal E1001` can perform hardware-level partial refresh under ESPHome.

As of 2026-04-23, this probe has completed verification, and the production business panel has been switched to `7.50inV2p`.

## Prerequisites

Before starting, confirm:

- This device can reliably display content with `7.50inv2alt + reset_duration: 2ms`
- The device has OTA capability
- You accept that this experimental YAML may still result in white screen, artifacts, full-screen flashing, or noticeable ghosting

If you have not completed these prerequisites, start with:
[reTerminal E1001 First Flash Runbook](./infohub-eink-first-flash-runbook.md)

## What the Probe Firmware Does

The probe firmware differs from the current business panel in two key ways:

1. Display driver model is switched to `7.50inV2p`
2. `full_update_every: 15` is enabled

According to the ESPHome documentation, `7.50inV2p` is the 7.5-inch V2 model variant that supports partial refresh. This firmware's purpose is to verify whether your actual display hardware can work stably with this configuration.

## How to Read the Display

The screen is divided into two areas:

- Left side: `STATIC REFERENCE`
  A border and checkerboard pattern that should remain unchanged over time
- Right side: `DYNAMIC BOX`
  A two-digit counter increments on a fixed cycle, and two small bars alternate below it

The bottom also displays:

- Current trigger source: `BOOT` / `AUTO` / `MANUAL` / `GPIO3` / `RESET`
- Current tick count
- Manual trigger count

## How to Verify

After flashing, watch for these three things:

1. Whether the dynamic box updates on schedule
2. Whether the left static area remains stable without a full-screen white flash each time
3. Whether full refreshes only occur occasionally at multiples of 15 (15th, 30th, etc.)

If the behavior looks like this, partial refresh is working:

- Right dynamic box changes are clearly visible
- Left checkerboard and border mostly remain stable during updates
- Only an occasional full-screen refresh every N cycles
- Ghosting is acceptable, no rapid degradation

If the behavior looks like this, this approach is not suitable for production:

- Every counter change causes a full-screen flash
- Static area is visibly redrawn repeatedly
- Severe ghosting, dirty refresh, or black borders appear quickly
- White screen or initialization failure

## Trigger Methods

You can observe behavior in three ways:

1. Wait for automatic updates
   Default interval is every `12s`.

2. Press the hardware button
   `GPIO3` manually advances one step.

3. Use ESPHome Dashboard buttons
   Two template buttons are available:
   - `Step`
   - `Reset Counter`

## Recommended Verification Sequence

1. OTA flash this probe firmware.
2. Observe 5 to 10 consecutive automatic updates.
3. Press `GPIO3` a few times to verify only the right area updates.
4. Wait at least until the 15th update to confirm a full refresh occurs as expected.
5. If overall verification passes, consider migrating the business panel to the same display model.

## Verification Results

The probe passed verification. The business firmware has been switched to `7.50inV2p + full_update_every: 15 + reset_duration: 2ms`.

To roll back, change the `model` back to `7.50inv2alt` in the business YAML and remove `full_update_every`.

## References

- ESPHome Waveshare E-Paper Display:
  [https://esphome.io/components/display/waveshare_epaper/](https://esphome.io/components/display/waveshare_epaper/)
- Seeed reTerminal E Series with ESPHome:
  [https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
