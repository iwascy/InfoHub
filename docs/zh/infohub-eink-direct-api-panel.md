# InfoHub EInk 直连 API 面板说明

> 当前文档对应第 2 阶段业务面板。
> 第一次 USB 首刷请先走 [reTerminal E1001 首刷 Runbook](./infohub-eink-first-flash-runbook.md)，确认设备、屏幕和 OTA 基础链路没问题后，再切到这里。
> 当前更推荐的编译/管理入口是 [Mac 上独立 ESPHome Docker 方案](./infohub-eink-esphome-docker-mac.md)。

这份方案是当前推荐路径的第 2 阶段：`reTerminal E1001 + ESPHome` 直接请求当前项目提供的设备接口。

核心数据接口：

- HTML 看板：`/dashboard/eink?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 调试 JSON：`/dashboard/eink.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 设备直连 JSON：`/dashboard/eink/device.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=300`

## 为什么改成直连 API

这条链路更适合你现在的要求：

1. 不走截图，不需要额外渲染服务
2. 面板直接消费当前项目 API，设备端不依赖浏览器渲染
3. ESPHome 只在 payload 变化时触发一次电子纸刷新，避免无意义的反复刷屏
4. 默认 Codex 版式为上方今日 / 本周 Token 用量，下方两个指定邮箱的额度及重置时间；不展示告警。旧版 DeepSeek + Codex 也可切换使用

## 当前仓库里对应的文件

- 设备接口：`GET /dashboard/eink/device.json`
- ESPHome 模板：[reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml)

## 1. 先确认项目接口

项目启动后，先验证三个入口：

```bash
curl "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
curl "http://10.30.5.172:8080/dashboard/eink.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
curl "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

设备接口返回的是更适合 ESPHome 解析的紧凑结构，包含：

- `updated_at`
- `codex`：仅 Codex 今日用量
- `template`：`trio`（默认）、`codex` 或 `deepseek-codex`
- `platforms`：`trio` 模板专用，依次为 Claude Pro、Codex、Antigravity；每项含 `name`、`today`（今日 Token）、`today_label`、`usage_note`（本周用量）和 `rows`（账号 / 模型分组的 `five_hour` / `week` 剩余额度，结构同 `codex_accounts`；`five_hour.reset_short` 只含时刻；上游没有该窗口时 `not_applicable` 为 true）
- `codex_week`：本周累计 Token，缺失或查询失败时为 `--`
- `daily_range` / `week_range`：用量统计区间
- `codex_accounts`：按邮箱白名单顺序排列的额度；未匹配账号仍保留 `--` 占位，窗口的 `reset_short` 使用北京时间
- `codex_rows` / `codex_focus` / `alerts` / `reset_hints`：兼容旧模板的完整额度和告警数据

`deepseek`、`deepseek_quota`、`total` 等历史字段仍保留兼容。`trio` / `codex` 模板不展示、不因它们变化刷新；`trio` 只按 `platforms` 判断是否刷新。升级正式业务 YAML 后，固件可根据接口的 `template` 字段选择三套布局；首次升级仍需编译刷机。

### 模板切换

在 `config.yaml` 设置 `dashboard.template: trio`（默认）、`codex`（只有 Codex）或 `deepseek-codex`（最早的混合版），重启服务后，网页和设备默认使用同一模板。请求参数可覆盖默认值：

```text
/dashboard/eink?template=trio&token=YOUR_TOKEN
/dashboard/eink?template=codex&token=YOUR_TOKEN
/dashboard/eink?template=deepseek-codex&token=YOUR_TOKEN
/dashboard/eink/device.json?template=deepseek-codex&token=YOUR_TOKEN
```

模板参数只影响当前请求，不修改全局配置。旧模板的 HTML 与固件存档见 `docs/panel-templates/codex/` 和 `docs/panel-templates/deepseek-codex/`。`trio` 设备版最多显示 3 个平台、每个平台 4 行。`dashboard.accounts` 控制 Codex 账号白名单；网页展示完整白名单，800×480 设备最多显示前两个账号。采集配置也必须包含这两个邮箱的 `account` target（通过账号 `email` / `credentials.email` / `extra.email` 精确匹配），否则显示 `--`。仓库默认配置已包含这两个 target，并保留旧版 Pro 20x 目标。

周用量按 `Asia/Shanghai` 的周一至今查询；使用 `user_id` 时只累计 `/responses` 的 Token，使用 `account_id` 时沿用今日账号统计口径。周数据单独写入 `token_usage_product_week`，不会混入今日用量。历史快照未采集过周统计时显示 `--`，等下一次采集即可。

## 2. ESPHome 设备走直连接口

在你已经完成 Stage 1 首刷之后，再使用 [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) 作为设备 YAML。

这个模板的关键点：

- `http_request.get` 直接拉 `device.json`
- `capture_response: true`，在设备端拿到完整 JSON body
- `max_response_buffer_size: 16384`，避免 1KB 默认缓冲过小
- `update_interval: never`，显示器不做固定周期刷新
- 仅 Codex 用量、额度、状态等可见数据变化时触发刷新；采集时间戳和 DeepSeek 变化不触发刷新。额度变化立即更新，不再等待多次局刷后的整包同步
- `GPIO3` 保留为实体手动刷新按钮
- `GPIO4` 同时作为电池 deep sleep 的手动唤醒键
- 还额外暴露了一个 `Force Sync` 按钮
- 电池 deep sleep 中按下 `GPIO4` 唤醒后，会强制同步一次，然后重新进入对应时段的 deep sleep
- 新增了“插电高实时 / 电池省电 / 电池夜间静默”三种运行状态
- Wi-Fi 使用 `fast_connect: true` 和 `power_save_mode: HIGH`，并关闭 fallback AP 自动启动，用于减少电池模式下的联网耗电
- 联网后不再固定等待 `5s`，Wi-Fi 或接口异常时由 `60s` 清醒超时兜底进入 deep sleep
- HTTP 请求超时从 `20s` 降到 `10s`，网络异常时少醒着等待

ESPHome 的 `secrets.yaml` 至少要补这些值：

```yaml
wifi_ssid: "YOUR_WIFI"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "YOUR_FALLBACK_PASSWORD"
# 可选：配合 YAML 中 manual_ip 使用，用于缩短唤醒后联网时间
wifi_static_ip: "10.30.5.173"
wifi_gateway: "10.30.5.1"
wifi_subnet: "255.255.255.0"
esphome_api_encryption_key: "YOUR_ESPHOME_API_KEY"
esphome_ota_password: "YOUR_OTA_PASSWORD"
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

你也可以直接从 [deploy/esphome/secrets.example.yaml](../../deploy/esphome/secrets.example.yaml) 复制示例，再填入真实值。

### 省电版轮询策略

当前仓库里的 API 模板使用周期唤醒策略：

- 插电模式：每 `2min` 请求一次
- 电池模式：每 `30min` 唤醒，请求和必要的屏幕刷新完成后立即 deep sleep
- 低电量模式：唤醒间隔自动延长到 `2h`
- 电池电压/电量采样：每次唤醒采样一次；插电常驻时每 `2min` 采样一次
- Power Profile 状态：每 `5min` 更新一次
- 电池夜间静默：`22:00` 到次日 `10:00` 不请求业务接口，并直接进入 deep sleep；等到 `10:00` 自动唤醒，或按 `GPIO4` 手动唤醒并刷新一次
- 电子纸刷新仍然保留“只有 payload 变化才刷新”的逻辑，所以插电模式虽然请求更频繁，但不会因为同一份内容反复刷屏
- 固件只持久化会影响画面的 payload 指纹和局刷计数；deep sleep 重启后如果业务内容未变化，不会重复刷屏
- `GPIO21` 只在电池采样的短时间内开启，采样完成后立即关闭
- 如果电量低于阈值，顶部状态栏会额外显示 `低电量` 标识

电池模式下 API/OTA 只在每次唤醒后的短窗口可用，而且成功同步后会立即休眠。需要 OTA 时，应先接入 USB 电源并确认设备进入“插电高实时”状态；`60s` 只是网络异常时的最长清醒兜底，不是固定的 OTA 窗口。

另外，模板还会额外暴露这些实体：

- `Battery Voltage`
- `Battery Level`
- `Power Profile`

### 供电模式如何判断

这版默认使用 Seeed 官方公开的电池测量方式：

- `GPIO21` 打开电池电压测量
- `GPIO1` 读取电池电压

再通过两个电压阈值做近似判断：

- `>= 4.15V` 视为插电高实时
- `<= 4.05V` 视为电池省电
- `<= 20%` 触发顶部 `低电量` 标识

这是一个“够实用、但不是绝对精确”的默认方案。原因是当前公开资料里比较明确的是电池电压采样能力，而不是一个现成的、已在当前仓库验证过的 USB/VBUS 供电脚位。

如果你后面实机发现：

- 满电拔电后一小段时间里仍被判成“插电”
- 或者边充边用但电压还没抬到阈值时，切换不够快

可以直接微调 [reterminal_e1001_infohub_api.yaml](../../deploy/esphome/reterminal_e1001_infohub_api.yaml) 顶部这些 substitution：

- `plugged_voltage_threshold`
- `battery_voltage_threshold`
- `low_battery_level_threshold`
- `battery_wake_interval_minutes`
- `low_battery_wake_interval_minutes`

### 已验证的配置注意事项

这两点是 2026-04-22 在真实 ESPHome 环境里已经踩到并确认过的问题：

- fallback AP 的 `ssid` 不能超过 32 个字符，所以不要继续用 `"${friendly_name} Fallback"` 这种长名字，当前模板已经改成 `InfoHub Fallback`
- `font.glyphs` 在 ESPHome 2026.4.1 下会严格校验重复字符，重复的空格、换行或汉字都会让 `esphome config` 直接失败；当前模板里的字形集合已经去重

当前 API 模板已通过 `esphome config` 校验并正常编译部署。

### 显示参数

当前这台 `reTerminal E1001` 的显示参数经过验证：

- 首刷使用 `7.50inv2alt + reset_duration: 2ms` 确认屏幕可点亮
- 业务固件已切到 `7.50inV2p + full_update_every: 15 + reset_duration: 2ms`，支持硬件级局部刷新
- 标准 `7.50inv2` 在当前设备上会白屏，不要使用

## 3. 关于”局部刷新”的当前结论

当前业务固件同时做到了两个层面的局部刷新：

1. **逻辑层面**：只有 API payload 变化时才触发屏幕刷新
2. **物理显示层面**：使用 `7.50inV2p` 支持硬件级 partial refresh，配合 `full_update_every: 15` 每 15 次局刷后做一次全刷，避免残影积累

## 4. 推荐的部署顺序

1. 先完成 [reTerminal E1001 首刷 Runbook](./infohub-eink-first-flash-runbook.md)，确认最小固件已 USB 刷入并且屏幕能亮字
2. 启动并确认 `collect-server` 的 `device.json` 可以访问
3. 把设备 YAML 切换成 API 直连版
4. 通过 OTA 更新设备，而不是重新走 USB 刷机
5. 验证只有 JSON 内容变化时才会重新刷屏
6. 如果启用了省电版模板，观察 `Power Profile` / `Battery Voltage` / `Battery Level` 三个实体，确认插电和电池切换是否符合这台机器的实际电压表现
7. 如果 `esphome config` 失败，先优先检查 Wi-Fi fallback 名称长度、`font.glyphs` 是否有重复字符，以及是否缺少根级 `json:` 组件

如果你准备进一步验证硬件级 partial refresh，不要直接拿业务面板硬切显示型号，先走独立探针固件：
[reTerminal E1001 局部刷新验证方案](./infohub-eink-partial-refresh-probe.md)

## 5. 如果还要继续省电

当前这版已经把全天电池周期唤醒和夜间 deep sleep 做进模板了。如果还要继续压榨续航，可以继续往下做：

- 如果后面确认到稳定可用的 USB/VBUS 检测脚位，可以把现在的电压近似判断改成真正的外部供电检测，切换会更准
- 如果后端数据变化不频繁，可以把 `battery_wake_interval_minutes` 从 `30` 调到 `60` 或更长
- 通过功耗仪测量每次唤醒耗时与 deep sleep 静态电流；如果深睡电流仍明显偏高，再排查板载稳压器和其他外设电源轨

## 参考资料

- Seeed 官方的 E1001 + ESPHome 基础接线和 `waveshare_epaper` 示例：
  [reTerminal E Series with ESPHome](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- ESPHome 官方 `waveshare_epaper` 组件文档：
  [Waveshare E-Paper Display](https://esphome.io/components/display/waveshare_epaper.html)
- ESPHome 官方 `http_request` 组件文档：
  [HTTP Request Component](https://esphome.io/components/http_request.html)
