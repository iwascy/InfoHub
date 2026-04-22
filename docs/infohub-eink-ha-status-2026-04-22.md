# InfoHub EInk + HA 当前状态（2026-04-22）

这份文档只做两件事：

- 把当前需求目标整理成统一口径
- 把已经完成、仍阻塞、下一步动作说明白

## 需求目标

当前目标已经收敛为这 5 件事：

1. 恢复 Home Assistant 可用状态
2. 尽量让 HA / Supervisor / 镜像拉取走代理，避免 add-on 下载过慢
3. 安装并启用 ESPHome
4. 用当前项目 API 驱动 `reTerminal E1001` 电子纸面板
5. 不走截图方案，尽量复刻现有 HTML 面板样式，并且只在数据变化时刷新

## 当前环境部署情况

当前这套环境不是“单机单服务”，而是 4 层拼起来的：

### 1. 宿主机

- 宿主机是 `Mac Studio`
- 当前项目工作目录：`/Users/cyan/code/collect-server`
- `collect-server` 面板/API 当前跑在宿主机地址：
  - `http://10.30.5.172:8080/dashboard/eink`
  - `http://10.30.5.172:8080/dashboard/eink/device.json`
- 本机代理端口：`10.30.5.172:7897`

### 2. Home Assistant OS 虚机

- HAOS 跑在 `UTM` 虚机里
- 当前虚机 UUID：
  `CE35710B-5A31-452A-BCE0-B4BF61155B5A`
- 当前虚机状态：`started`
- 当前主 IPv4：`10.30.5.227`
- HA Web 入口：`http://10.30.5.227:8123`

### 3. Home Assistant / ESPHome 部署位

- HA 已完成 onboarding，可以正常打开
- `InfoHub EInk` Lovelace dashboard 已经注册到 HA
- ESPHome add-on 已安装并启动：
  - slug：`5c53de3b_esphome`
  - version：`2026.4.1`
- ESPHome 配置目录位于 guest：
  `/mnt/data/supervisor/homeassistant/esphome`
- Wi-Fi 真实凭据已经写入 guest 内的 `secrets.yaml`
  - 不再是占位值
  - 文档里不记录明文

### 4. 设备侧现状

- 当前设备是 `reTerminal E1001`
- 在 `SenseCraft HMI` 后台里已经能看到对应设备在线
- 当前看到的设备编号是 `1001`
- 当前 HMI 页面里已经能加载同一份 `AI 额度监控面板` 内容，说明面板视觉稿和数据源都已有运行中的参考环境
- 当前设备电量很低，看到过约 `3%`
  - 这会影响后续连续调试和刷写稳定性，后续操作时要优先保证供电

## 当前结论

### 1. HA 主体已经恢复

- HAOS 已恢复正常启动，不再反复掉进 rescue
- HA Web 已可访问
- `InfoHub EInk` Lovelace 仪表盘已经成功注册并显示在左侧栏
- iframe 看板可以正常加载项目里的 HTML 面板

### 2. 代理链路已经部分打通

- DNS 持久化配置已经改为国内上游 DNS
- 这一步已经解决此前 `ghcr.io` 解析超时导致 add-on 镜像拉取卡住的问题
- 当前 boot 内，`docker` 和 `hassos-supervisor` 的 runtime proxy 已经生效
- 还没有确认到 HAOS 官方支持的“Docker / Supervisor 持久化全局代理”方案

### 3. ESPHome add-on 已安装并运行

- ESPHome add-on 已安装并处于 `started`
- add-on 容器内部的 dashboard socket 已经监听
- 说明 ESPHome 服务本身是起来了，不是 add-on 没跑
- 但通过 `utmctl exec + docker exec` 触发长时间 compile 时，执行链本身不稳定，不能把“CLI 中断”直接等同于“ESPHome 配置错误”

### 4. API 直连方案已接通

当前项目已经提供：

- HTML 看板：`/dashboard/eink`
- 调试 JSON：`/dashboard/eink.json`
- 设备直连 JSON：`/dashboard/eink/device.json`

其中设备侧已经按“直连 JSON + 本地排版渲染”的路线实现，不再依赖截图。

### 5. ESPHome API 版模板已经修到可校验

本轮确认并修掉了 4 个真实问题，其中前 3 个会直接影响编译或配置校验，最后 1 个会影响最终屏幕显示：

- fallback AP 名称过长
- `font.glyphs` 存在重复字符
- 使用 `json::parse_json(...)` 但缺少根级 `json:` 组件声明
- `font.glyphs` 没有覆盖界面实际会显示的全部字符，后续会导致缺字

修完后，API 版模板已经通过 `esphome config` 校验。

### 6. 编译并不是停在 YAML 校验阶段

当前进一步确认到：

- `build/reterminal-e1001-infohub` 目录已经生成
- `src/main.cpp`、`platformio.ini`、`sdkconfig.*` 等文件都已经生成
- 说明 ESPHome 至少已经跑过 `Generating C++ source...`
- 但 `.pioenvs` 目录仍然是空的，`firmware.bin` 也还没有出现

这更像是：

- 编译过程在“进入 PlatformIO 真正产物阶段前”被打断
- 或者 `utmctl exec + docker exec` 这条执行链在长任务场景下提前中断

不能简单下结论说“YAML 还有语法问题”。

### 7. 白屏根因已经定位到显示初始化参数

2026-04-22 当天补充验证后，当前这台 `reTerminal E1001` 的首刷闭环已经拿到一个明确结论：

- 通过浏览器 Web Serial 首刷已经成功
- 标准 `waveshare_epaper + 7.50inv2` 刷入后会全白屏
- 改用 [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml)
- 并使用 `model: 7.50inv2alt` + `reset_duration: 2ms`

后，屏幕已经可以正常显示。

这说明当前主阻塞已经从“怎么点亮屏”切换成“把业务面板切到同一套显示参数并完成 OTA / API 联调”。

## 当前仍阻塞的点

### 1. 首刷点亮已经完成，当前进入 Stage 2 联调

当前“真正点亮面板”这一步已经完成，剩余动作变成：

1. 用已验证的 `7.50inv2alt + reset_duration: 2ms` 参数组更新业务面板
2. 验证设备拉取 `/dashboard/eink/device.json`
3. 验证 Home Assistant 发现与接管
4. 验证只有数据变化时才刷新

### 2. 当前最大阻塞是“编译执行链不稳定”

现在的主要问题已经不是：

- HA 没起来
- add-on 没装
- Wi-Fi 没写

而是旧链路里：

- `utmctl exec` 对长时间命令不稳定
- `docker exec addon_5c53de3b_esphome esphome compile ...` 经常出现“日志写到一半，但执行链提前断掉”

不过当前主线已经切到 `Mac + OrbStack + 独立 ESPHome Docker`，并且 `reterminal_e1001_infohub_api.yaml` 已经在这条链路下成功编译出 `firmware.factory.bin` 和 `firmware.ota.bin`。

### 3. 当前设备仍未完成“切到业务 ESPHome 固件”这一步

当前运行中的在线设备环境，更多还是：

- `SenseCraft HMI` 侧已有参考面板和在线设备
- HA 侧已经有同名 dashboard

但“设备已经切到 ESPHome 版直连 API 业务固件”这一步，还没有完成闭环。

### 4. 真正硬件级 partial refresh 还不能先拍板

目前已经能保证的是：

- API payload 不变就不触发刷新

但是否支持硬件级 partial refresh，还取决于屏幕批次和最终使用的 `waveshare_epaper` 型号；当前配置保持稳妥路线，不先对硬件 partial refresh 做过度承诺。

## 建议的后续动作

下一步按这个顺序推进：

1. 保持当前主线在 Mac 本机 Docker / OrbStack，不再回到 `utmctl exec + docker exec`
2. 用已经验证可亮屏的显示参数更新业务 YAML，并优先通过 OTA 刷入
3. 验证设备在线、HA 发现、API 拉取、屏幕刷新
4. 在确认供电稳定后，再评估是否需要继续做连续调试
5. 最后再视屏幕批次决定要不要尝试更激进的 partial refresh 驱动型号

## 相关文件

- API 版 ESPHome 模板：[reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)
- 旧版截图思路模板：[reterminal_e1001_infohub.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub.yaml)
- 直连方案说明：[infohub-eink-direct-api-panel.md](/Users/cyan/code/collect-server/docs/infohub-eink-direct-api-panel.md)
