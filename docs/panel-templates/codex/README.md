# Codex

- Template ID: `codex`
- Source revision: `5cb484b04d14a06c0a48541c97438b919f125b86` plus the uncommitted Codex-only redesign
- `dashboard.html.tmpl`: Go HTML template, copied verbatim from `internal/api/dashboard_codex.html` (formerly `dashboard.html`).
- `reterminal_e1001_infohub_api.yaml`: ESPHome business panel at the time of the archive (renders `codex` and `deepseek-codex`).
- `dashboard.png`: mock-data preview.

This archive preserves the Codex-only layout before the `trio` (Claude · Codex · Antigravity) redesign. The active HTML copy is embedded from `internal/api/dashboard_codex.html`; select it with `?template=codex` or `dashboard.template: codex`. The current business ESPHome YAML still draws this layout when the device response's `template` field is `codex`. No credentials are included; the ESPHome file still references the existing secrets file.
