# InfoHub EInk 面板落地说明

> 这份文档保留作旧截图链路的历史参考，不再是当前主线方案。
> 当前主线请优先看：
> [reTerminal E1001 首刷 Runbook](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)
> 和 [InfoHub EInk 直连 API 面板说明](/Users/cyan/code/collect-server/docs/infohub-eink-direct-api-panel.md)

这套方案的目标是尽量少写设备侧排版代码，直接复用当前项目已经提供好的看板页面：

- HTML 看板：`/dashboard/eink?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`
- JSON 调试接口：`/dashboard/eink.json?token=<INFOHUB_DASHBOARD_TOKEN>&refresh=600`

推荐链路：

1. `collect-server` 提供 HTML 看板
2. `Home Assistant` 用一个单页 dashboard 通过 `iframe` 嵌入该看板
3. `Puppet` add-on 对该 dashboard 截图
4. `reTerminal E1001 + ESPHome` 定时拉取截图并显示到电子纸

这样可以直接复用当前项目里的排版和数据计算逻辑，最终效果会最接近仓库中已有的 `eink` 页面与截图。

## 1. 准备 InfoHub 看板地址

确保当前项目服务已启动，并且 Home Assistant 所在局域网可以访问：

```bash
curl "http://10.30.5.172:8080/dashboard/eink?token=YOUR_DASHBOARD_TOKEN&refresh=600"
```

如果要验证结构化数据接口：

```bash
curl "http://10.30.5.172:8080/dashboard/eink.json?token=YOUR_DASHBOARD_TOKEN&refresh=600"
```

默认端口来自项目配置，未显式设置时是 `8080`。

## 2. 在 Home Assistant 里挂一个专用 Dashboard

把 [deploy/homeassistant/configuration/infohub_dashboard_registration.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/configuration/infohub_dashboard_registration.yaml) 合并进你的 `configuration.yaml`。

把 [deploy/homeassistant/dashboards/infohub_eink.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/dashboards/infohub_eink.yaml) 放到 HA 的 `/config/dashboards/infohub_eink.yaml`。

再把 [deploy/homeassistant/secrets.example.yaml](/Users/cyan/code/collect-server/deploy/homeassistant/secrets.example.yaml) 里的示例条目复制到 HA 的 `secrets.yaml`，并改成你真实的 `dashboard_token`。

完成后，HA 里会出现一个新的 `InfoHub EInk` dashboard，页面路径通常是：

```text
/infohub-eink/default_view
```

## 3. 安装 Puppet 并验证截图地址

按照 Seeed 的官方做法安装 `Puppet` add-on，并给它配置一个 Home Assistant 的 Long-Lived Access Token。

官方参考：

- Seeed 的高级示例说明了 `Puppet` 的工作方式和截图 URL 规则：[ESPHome Advanced Usage](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome_advanced/)
- 该文档给出的截图地址格式是：`http://homeassistant.local:10000/<page>?viewport=800x480&eink=2&invert`

对于这套 dashboard，对应截图地址通常会是：

```text
http://10.30.5.227:10000/infohub-eink/default_view?viewport=800x480&eink=2&invert
```

先在浏览器里打开这条地址，确认能看到黑白截图。

如果黑白反了，删掉 `&invert` 再试。

## 4. 刷入 reTerminal E1001 的 ESPHome 固件

把 [deploy/esphome/reterminal_e1001_infohub.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub.yaml) 作为 `ESPHome` 设备 YAML。

需要按你的环境替换的部分：

- `puppet_base_url`
- `puppet_page_path`
- `wifi_ssid`
- `wifi_password`
- `wifi_fallback_password`
- `esphome_api_encryption_key`
- `esphome_ota_password`

默认配置说明：

- 设备型号按 Seeed 官方示例使用 `waveshare_epaper` + `model: 7.50inv2`
- 截图地址使用 `800x480`，正好匹配 E1001 分辨率
- `GPIO3` 绿键被配置为手动刷新按钮
- 如果复杂页面出现显示异常，可把 `model` 改成 `7.50inv2alt`

官方硬件与截图方案参考：

- 基础 E1001 显示配置：[reTerminal E Series + ESPHome 基础用法](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome/)
- 截图显示方案：[reTerminal E Series + ESPHome 高级用法](https://wiki.seeedstudio.com/reterminal_e10xx_with_esphome_advanced/)

## 5. 建议的实际部署顺序

1. 先解决 `HAOS /mnt/data` 空间不足问题
2. 确认 `collect-server` 在 Mac Studio 上稳定运行
3. 在 Home Assistant 中先把 `InfoHub EInk` dashboard 跑起来
4. 安装并验证 `Puppet`
5. 首次通过 USB 给 E1001 刷入 ESPHome
6. 验证截图链路正常后，再改走 OTA

## 备注

这套方案的优点是：

- 设备端逻辑最薄
- 面板样式完全由当前项目控制
- 后续你改 Go 侧 HTML，就能直接影响电子纸展示

如果后续你更想走“ESPHome 直接画表格，不依赖截图”的路线，当前新增的 `/dashboard/eink.json` 也可以继续作为第二阶段的数据源。
