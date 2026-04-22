# InfoHub EInk 直连 API 面板说明

> 当前文档对应第 2 阶段业务面板。
> 第一次 USB 首刷请先走 [reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)，确认设备、屏幕和 OTA 基础链路没问题后，再切到这里。
> 当前更推荐的编译/管理入口是 [Mac 上独立 ESPHome Docker 方案](/Users/cyan/code/collect-server/docs/infohub-eink-esphome-docker-mac.md)。

这份方案是当前推荐路径的第 2 阶段：`reTerminal E1001 + ESPHome` 直接请求当前项目提供的设备接口，不再依赖 HA 截图链路。

核心数据接口：

- HTML 看板：`/dashboard/eink?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 调试 JSON：`/dashboard/eink.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- 设备直连 JSON：`/dashboard/eink/device.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=300`

## 为什么改成直连 API

这条链路更适合你现在的要求：

1. 不走截图，不需要 `Puppet`
2. 面板直接消费当前项目 API，设备端不依赖浏览器渲染
3. ESPHome 只在 payload 变化时触发一次电子纸刷新，避免无意义的反复刷屏
4. 看板页面仍然可以继续接入 Home Assistant 的 iframe dashboard，方便在 HA 里看同一份数据
5. 设备侧版式按当前 HTML 看板做高保真复刻，保持三张概览卡片、双表格和右侧提醒栏的同一视觉结构

## 当前仓库里对应的文件

- 设备接口：`GET /dashboard/eink/device.json`
- ESPHome 模板：[reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)
- HA iframe 看板注册：
  [infohub_dashboard_registration.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/configuration/infohub_dashboard_registration.yaml)
  [infohub_eink.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/dashboards/infohub_eink.yaml)

## 1. 先确认项目接口

项目启动后，先验证三个入口：

```bash
curl "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
curl "http://10.30.5.172:8080/dashboard/eink.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
curl "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

设备接口返回的是更适合 ESPHome 解析的紧凑结构，包含：

- `updated_at`
- `claude`
- `sub2api`
- `total`
- `claude_rows`
- `sub2api_rows`
- `alerts`
- `reset_hints`

## 2. 在 Home Assistant 里保留一个 iframe 看板

如果你希望在 HA 里也能看同一份内容，可以继续保留 HTML dashboard：

1. 把 [infohub_dashboard_registration.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/configuration/infohub_dashboard_registration.yaml) 合并进 HA 的 `configuration.yaml`
2. 把 [infohub_eink.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/dashboards/infohub_eink.yaml) 放到 `/config/dashboards/infohub_eink.yaml`
3. 在 HA 的 `secrets.yaml` 里加入：

```yaml
infohub_eink_source_url: "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
```

这样 HA 里会有一个 `InfoHub EInk` dashboard，但这只是辅助查看，不再参与设备渲染。

## 3. ESPHome 设备改走直连接口

在你已经完成 Stage 1 首刷之后，再使用 [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml) 作为设备 YAML。

这个模板的关键点：

- `http_request.get` 直接拉 `device.json`
- `capture_response: true`，在设备端拿到完整 JSON body
- `max_response_buffer_size: 16384`，避免 1KB 默认缓冲过小
- `update_interval: never`，显示器不做固定周期刷新
- 只要 HTTP 返回 body 和上次完全一致，就不触发 `component.update`
- `GPIO3` 保留为实体手动刷新按钮
- 还额外暴露了一个 HA 里的 `Force Sync` 按钮

ESPHome 的 `secrets.yaml` 至少要补这些值：

```yaml
wifi_ssid: "YOUR_WIFI"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "YOUR_FALLBACK_PASSWORD"
esphome_api_encryption_key: "YOUR_ESPHOME_API_KEY"
esphome_ota_password: "YOUR_OTA_PASSWORD"
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

你也可以直接从 [deploy/esphome/secrets.example.yaml](/Users/cyan/code/collect-server/deploy/esphome/secrets.example.yaml) 复制示例，再填入真实值。

### 已验证的配置注意事项

这两点是 2026-04-22 在真实 HA / ESPHome 环境里已经踩到并确认过的问题：

- fallback AP 的 `ssid` 不能超过 32 个字符，所以不要继续用 `"${friendly_name} Fallback"` 这种长名字，当前模板已经改成 `InfoHub Fallback`
- `font.glyphs` 在 ESPHome 2026.4.1 下会严格校验重复字符，重复的空格、换行或汉字都会让 `esphome config` 直接失败；当前模板里的字形集合已经去重

按当前仓库里的 API 版模板，`esphome config reterminal_e1001_infohub_api.yaml` 已经可以通过校验。

另外，2026-04-22 在当前这台 `reTerminal E1001` 上已经实机确认：

- 首刷标准 `7.50inv2` 会出现全白屏
- 改成 `7.50inv2alt`
- 并加上 `reset_duration: 2ms`

之后屏幕即可正常显示。

所以当前仓库里的 API 模板已经同步切到这套显示参数，避免 Stage 1 能亮、Stage 2 又回退成白屏。

## 4. 关于“局部刷新”的实际结论

这里要如实区分两层含义：

1. 逻辑层面
   现在这份配置已经做到“局部数据更新”：
   只有 API payload 真的变了，设备才会再次刷新屏幕。

2. 物理显示层面
   `reTerminal E1001` 常见官方示例仍然是 `waveshare_epaper` + `model: 7.50inv2`。但当前这台设备实测需要 `7.50inv2alt + reset_duration: 2ms` 才能稳定显示。而 ESPHome 官方把支持 partial refresh 的 7.5 寸型号单独列成 `7.50inV2p`。

所以当前更稳妥的判断是：

- 这套方案能做到“API 直连 + 仅变化时刷新”
- 但不能默认承诺这块屏一定支持真正意义上的硬件 partial refresh
- 只有在你确认自己这块屏对应的是支持 partial refresh 的具体批次时，才建议尝试改成 `7.50inV2p`

## 5. 推荐的部署顺序

1. 先完成 [reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)，确认最小固件已 USB 刷入并且屏幕能亮字
2. 保持现在的 HAOS 空间状态，先不要急着装更多 add-on
3. 启动并确认 `collect-server` 的 `device.json` 可以访问
4. 在 HA 里先把 iframe dashboard 挂好，方便直接验证 token 和页面
5. 再把设备 YAML 切换成 API 直连版
6. 通过 OTA 更新设备，而不是重新走 USB 刷机
7. 验证只有 JSON 内容变化时才会重新刷屏
8. 如果 `esphome config` 失败，先优先检查 Wi-Fi fallback 名称长度、`font.glyphs` 是否有重复字符，以及是否缺少根级 `json:` 组件

## 参考资料

- Seeed 官方的 E1001 + ESPHome 基础接线和 `waveshare_epaper` 示例：
  [reTerminal E Series with ESPHome](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- ESPHome 官方 `waveshare_epaper` 组件文档：
  [Waveshare E-Paper Display](https://esphome.io/components/display/waveshare_epaper.html)
- ESPHome 官方 `http_request` 组件文档：
  [HTTP Request Component](https://esphome.io/components/http_request.html)
