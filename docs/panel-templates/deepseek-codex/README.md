# DeepSeek + Codex

- Template ID: `deepseek-codex`
- Source revision: `5cb484b04d14a06c0a48541c97438b919f125b86`
- `dashboard.html.tmpl`: original Go HTML template, extracted verbatim from `internal/api/dashboard.go`.
- `reterminal_e1001_infohub_api.yaml`: original ESPHome business panel, preserved verbatim.

This archive preserves the mixed-product layout before the Codex-only redesign. The active HTML copy is embedded from `internal/api/dashboard_deepseek_codex.html`; select it with `?template=deepseek-codex` or `dashboard.template: deepseek-codex`. The current business ESPHome YAML also includes this layout and selects it from the device response's `template` field. Use the current business YAML for runtime switching; this archived firmware remains unchanged for reference. No credentials are included; the ESPHome file still references the existing secrets file.
