# reTerminal E1001 首刷 Runbook

这份 runbook 对应当前主线方案的第 1 阶段：先把 `reTerminal E1001` 稳定刷成可 OTA 的 ESPHome 设备，再切换到 InfoHub 业务面板。

当前推荐走 Mac 本机独立 Docker 方案：
[Mac 上独立 ESPHome Docker 方案](/Users/cyan/code/collect-server/docs/infohub-eink-esphome-docker-mac.md)

目标只有 4 个：

1. 拿到一个稳定可编译的 `factory` 固件
2. 首次通过 USB 成功刷入设备
3. 看到电子纸屏正常显示
4. 让设备通过 Wi-Fi 出现在 ESPHome Dashboard 里

等这 4 件事完成，再切到 API 直连面板：[infohub-eink-direct-api-panel.md](/Users/cyan/code/collect-server/docs/infohub-eink-direct-api-panel.md)

## 已确认的硬件结论

2026-04-22 在当前这台 `reTerminal E1001` 上，首刷测试已经得到明确结论：

- 标准 `7.50inv2` 配置刷入后会出现全白屏
- 改用 [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml) 后，屏幕可以正常显示
- 当前设备后续所有业务 YAML 都应沿用同一套显示初始化参数：
  `model: 7.50inv2alt` + `reset_duration: 2ms`

所以这份 runbook 里提到的 `alt` 配置，不再只是“兜底尝试”，而是当前设备的已验证可用配置。

## 为什么先做最小首刷

首刷拆成两阶段：

- 第 1 阶段只验证硬件、USB、编译、Wi-Fi、ESPHome 在线
- 第 2 阶段才加载 [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml) 做 API 拉取和页面排版

这样排障面会小很多。

## 0. 首刷前检查

开始前先确认：

- 设备已经接上稳定供电，不要在低电量状态下刷机
- 使用的是 `2.4GHz Wi-Fi`
- Mac 上已经具备 USB 串口驱动环境

如果设备休眠或黑屏，先按背面的唤醒键。

## 1. 复制 Stage 1 文件

本仓库已经准备好首刷用文件：

- 推荐首刷 YAML（当前设备已验证可用）：
  [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
- secrets 示例：
  [secrets.example.yaml](/Users/cyan/code/collect-server/deploy/esphome/secrets.example.yaml)

> 仓库里还有一份 `reterminal_e1001_first_flash.yaml`（标准 `7.50inv2`），但当前这台设备用它会白屏，不要使用。

把 `secrets.example.yaml` 的内容复制到 ESPHome 设备目录里的 `secrets.yaml`，至少填这 5 项：

```yaml
wifi_ssid: "YOUR_2.4G_WIFI_SSID"
wifi_password: "YOUR_WIFI_PASSWORD"
wifi_fallback_password: "CHANGE_ME_123"
esphome_api_encryption_key: "REPLACE_WITH_32_BYTE_BASE64_KEY"
esphome_ota_password: "REPLACE_WITH_OTA_PASSWORD"
```

这一阶段不需要 `infohub_eink_device_url`。

如果你还没有 API 加密 key 和 OTA 密码，可以在宿主机先生成：

```bash
openssl rand -base64 32
openssl rand -hex 16
```

通常第一条用于 `esphome_api_encryption_key`，第二条可作为 `esphome_ota_password`。

## 2. 在 ESPHome UI 里导入最小 YAML

推荐做法：

1. 按 [Mac 上独立 ESPHome Docker 方案](/Users/cyan/code/collect-server/docs/infohub-eink-esphome-docker-mac.md) 启动本地 `ESPHome Dashboard`
2. 打开 `http://localhost:6052`
3. 新建或编辑设备
4. 当前这台设备优先粘贴 [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
5. 保存
6. 通过 Dashboard 触发安装，优先选择生成 `factory` 固件的下载流程

当前阶段只要拿到首个 `factory` 固件即可。

## 3. 首次 USB 刷机

第一次刷机建议走官方浏览器/USB 路线：

1. 用 USB 连接 `reTerminal E1001`
2. 在浏览器里打开 ESPHome 的 Web 安装页或 ESPHome UI 导出的安装入口
3. 选择串口
4. 写入刚才拿到的 `factory` 固件

如果浏览器里看不到串口：

- 先确认 USB 线支持数据传输
- 确认设备已经被唤醒
- 确认 Mac 上串口驱动已经安装

## 4. 通过什么结果判断 Stage 1 成功

首刷成功后，屏幕应该能显示一个非常简单的诊断页，核心信息包括：

- `ALT PROFILE`
- `7.50inv2alt + reset_duration 2ms`
- 如果 Wi-Fi 已连上，会显示 `SSID` 和 `IP`
- 如果 Wi-Fi 还没连上，会显示 `WiFi pending` 和 fallback AP 名称

另外：

- `GPIO3` 被保留为手动重绘按钮
- ESPHome Dashboard 里应能看到设备在线

只要这一步成功，就说明：

- USB 首刷闭环通了
- 屏幕驱动脚位通了
- 当前设备已经确认 `waveshare_epaper + 7.50inv2alt + reset_duration: 2ms` 可以正常点亮
- 设备已经具备后续 OTA 的基础条件

## 5. Stage 1 成功后再切 Stage 2

确认 Stage 1 没问题后，再把设备 YAML 切换到：

- [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)

然后在 `secrets.yaml` 里补上：

```yaml
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

Stage 2 再验证这些业务目标：

1. 设备能拉到 `/dashboard/eink/device.json`
2. JSON 变更时才刷新电子纸
3. 之后改走 OTA，而不是重复 USB 刷机

如果你想继续确认这块屏是否支持硬件级局部刷新，建议不要直接在业务 YAML 上冒险，先刷独立探针：
[reTerminal E1001 局部刷新验证方案](/Users/cyan/code/collect-server/docs/infohub-eink-partial-refresh-probe.md)

## 6. 常见故障怎么切分

### A. 编译拿不到 `factory` 固件

优先怀疑：

- ESPHome UI 本身报错
- YAML / secrets 配置问题
- 字体下载或外网拉取异常

优先在 ESPHome Dashboard 里排查。

### B. 固件能下载，但 USB 写不进去

优先怀疑：

- USB 线只有充电没有数据
- 设备未唤醒
- 串口驱动未安装
- 浏览器没拿到串口权限

### C. 能刷进去，但屏幕不亮

优先怀疑：

- 设备供电不稳
- 设备还没真正重启完成
- 当前批次屏幕初始化时序不稳定

先只看这份 Stage 1 YAML，不要同时调业务接口。

如果刷完后屏幕一直全白，优先不要继续怀疑 Wi-Fi 或 API。

Stage 1 文案显示本身不依赖网络，”全白”是显示初始化参数不匹配。确认使用的是 `reterminal_e1001_first_flash_alt.yaml`（`7.50inv2alt + reset_duration: 2ms`），不要使用标准 `7.50inv2`。

### D. 屏亮了，但 Wi-Fi 没上来

优先怀疑：

- 用了 `5GHz` Wi-Fi
- SSID 或密码写错
- 信号太弱

这种情况下先看屏上的 `WiFi pending`，不要急着切业务面板。

## 7. 相关文件

- 推荐首刷配置：[reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
- 业务面板配置：[reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)
- API 直连说明：[infohub-eink-direct-api-panel.md](/Users/cyan/code/collect-server/docs/infohub-eink-direct-api-panel.md)
