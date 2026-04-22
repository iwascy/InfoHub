# Mac 上独立 ESPHome Docker 方案

这套方案的目标是把 `ESPHome` 从 `Home Assistant OS` 虚机里拆出来，直接跑在 `Mac Studio` 的 Docker Desktop 上。

这样做的好处很直接：

- 编译链不再依赖 `UTM + HAOS + add-on`
- `ESPHome Dashboard` 独立可用，出问题更容易定位
- 设备仍然可以继续接入 `Home Assistant`
- 当前的 `reTerminal E1001` 业务面板本来就是直接拉项目 API，并不依赖 HA 截图链路

## 适用边界

这套方案适合：

- 在 Mac 上稳定编译 ESPHome
- 打开本地 ESPHome Dashboard
- 生成 `factory` 固件
- 首次通过浏览器/USB 给 `reTerminal E1001` 刷机
- 后续通过 OTA 更新到业务面板

这套方案不做的事：

- 不用 Docker 直接接管 USB 首刷

原因是 `Docker Desktop on macOS` 不适合把宿主 USB 设备直接透传进 ESPHome 容器。当前推荐做法是：

1. 用 Docker 里的 ESPHome 编译固件
2. 从 Dashboard 手动下载 `factory` 固件
3. 用浏览器 Web Serial 或其它宿主侧 USB 工具刷到设备

这和 ESPHome 官方在 macOS 下对 Docker 的使用方式是一致的。

## 目录结构

当前仓库已经整理成下面这套结构：

```text
/Users/cyan/code/collect-server/
├── Makefile
├── deploy/
│   └── esphome/
│       ├── docker/
│       │   ├── compose.yaml
│       │   └── .env.example
│       ├── secrets.example.yaml
│       ├── reterminal_e1001_first_flash.yaml
│       ├── reterminal_e1001_first_flash_alt.yaml
│       ├── reterminal_e1001_infohub_api.yaml
│       └── .esphome/              # 编译缓存，已加入 .gitignore
└── docs/
    ├── infohub-eink-first-flash-runbook.md
    └── infohub-eink-direct-api-panel.md
```

其中：

- [compose.yaml](/Users/cyan/code/collect-server/deploy/esphome/docker/compose.yaml) 负责启动本地 `ESPHome Dashboard`
- [secrets.example.yaml](/Users/cyan/code/collect-server/deploy/esphome/secrets.example.yaml) 是设备侧 secrets 模板
- [reterminal_e1001_first_flash.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash.yaml) 用于第 1 阶段首刷
- [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml) 是当前这台 E1001 已验证可亮屏的首刷配置
- [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml) 用于第 2 阶段业务面板

注意：

- `/config` 仍然映射到仓库里的 `deploy/esphome`
- `PlatformIO` 包缓存会走容器卷 `/cache`
- 编译产物会走容器卷 `/build`

这样可以避开 OrbStack/macOS 共享目录在处理 `ESP-IDF` 大量文件时的复制失败问题。

## 一次性准备

在仓库根目录执行：

```bash
cd /Users/cyan/code/collect-server
cp deploy/esphome/secrets.example.yaml deploy/esphome/secrets.yaml
cp deploy/esphome/docker/.env.example deploy/esphome/docker/.env
```

然后编辑：

- `deploy/esphome/secrets.yaml`
- `deploy/esphome/docker/.env`

最少需要改的内容：

```yaml
# deploy/esphome/secrets.yaml
wifi_ssid: "你的 2.4G Wi-Fi"
wifi_password: "你的 Wi-Fi 密码"
wifi_fallback_password: "建议单独设一个"
esphome_api_encryption_key: "openssl rand -base64 32 生成"
esphome_ota_password: "openssl rand -hex 16 生成"
```

如果你还没有 key/password：

```bash
openssl rand -base64 32
openssl rand -hex 16
```

## 可执行命令

下面这些命令都可以直接在仓库根目录执行：

如果你使用的是 `OrbStack`，建议在命令前显式加上：

```bash
DOCKER_CONTEXT=orbstack
```

例如：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-up
```

### 1. 校验 compose

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-config
```

### 2. 拉取镜像

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-pull
```

### 3. 启动 ESPHome Dashboard

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-up
```

启动后，本地地址默认是：

```text
http://localhost:6052
```

### 4. 查看日志

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-logs
```

### 5. 查看容器状态

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-ps
```

### 6. 停掉 Dashboard

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-down
```

### 7. 变更 compose 后重建容器

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-recreate
```

## 两条使用路径

### 路径 A：推荐，用 Dashboard 做首刷

这是当前最稳妥的主路径。

1. 运行：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-up
```

2. 打开：

```text
http://localhost:6052
```

3. 在 Dashboard 里导入或编辑：

- 当前这台设备优先使用 [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml)

4. 选择手动下载 `factory` 固件
5. 用浏览器 Web Serial 或宿主机 USB 工具完成第一次刷机
6. 屏幕显示 `ALT PROFILE` 后，再切到：

- [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)

### 路径 B：用 CLI 先做编译验证

如果你想先排除 YAML/字体/依赖问题，可以先只跑编译：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-compile-stage1-alt
```

Stage 2 业务面板编译：

```bash
cd /Users/cyan/code/collect-server
make DOCKER_CONTEXT=orbstack esphome-compile-stage2
```

这两条命令的目标是：

- 先在 Mac 上确认编译链正常
- 不再依赖 `UTM + docker exec`

这里的 CLI 编译会直接在已经运行的 `infohub-esphome` 容器里执行，并显式复用镜像里的 `/entrypoint.sh`。
这样可以避免多个容器并发写同一套 `PlatformIO` 缓存时出现包状态损坏。
同时也能确保 `/cache` 和 `/build` 这些容器卷配置在 CLI 编译时同样生效。

## 推荐的实际操作顺序

### 第 1 阶段：先让设备亮起来

1. 准备 `deploy/esphome/secrets.yaml`
2. 执行 `make DOCKER_CONTEXT=orbstack esphome-up`
3. 打开 `http://localhost:6052`
4. 用 [reterminal_e1001_first_flash_alt.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_first_flash_alt.yaml) 生成 `factory` 固件
5. 第一次通过浏览器/USB 刷进设备
6. 确认屏幕出现 `ALT PROFILE`

### 第 2 阶段：再切业务面板

1. 在 `deploy/esphome/secrets.yaml` 里补上：

```yaml
infohub_eink_device_url: "http://10.30.5.172:8080/dashboard/eink/device.json?token=YOUR_DASHBOARD_TOKEN&refresh=300"
```

2. 把设备切到 [reterminal_e1001_infohub_api.yaml](/Users/cyan/code/collect-server/deploy/esphome/reterminal_e1001_infohub_api.yaml)
3. 通过 OTA 更新
4. 验证只有 JSON 变化时才刷新

## 常见问题

### 1. 为什么 Compose 里开了 `ESPHOME_DASHBOARD_USE_PING=true`

ESPHome 官方文档专门提到 Docker on Mac 应该打开这个选项，这样 Dashboard 的设备在线检查更稳。

### 2. 为什么额外挂了 `/cache` 和 `/build`

ESPHome 官方镜像的 `entrypoint.sh` 会在检测到 `/cache` 和 `/build` 挂载时：

- 把 `PlatformIO` 的平台/包/缓存放到 `/cache`
- 把编译输出放到 `/build`

这对 OrbStack/macOS 很重要，因为 `ESP-IDF` 安装过程中包含大量文件复制。把这些高频 I/O 从共享目录 `/config` 挪开后，稳定性会明显更好。

### 3. 为什么不直接用 Docker 容器刷 USB

因为你现在在 macOS 上跑的是 Docker Desktop，这一层本身就带了 Linux 虚拟化。把 USB 稳定透传到 ESPHome 容器里并不是这条路线的强项。

当前更稳的组合是：

- Docker 负责编译和 Dashboard
- 浏览器/宿主机负责第一次 USB 刷机
- 设备上线后改走 OTA

### 4. 代理怎么配

如果字体下载、依赖下载慢，可以在：

- [deploy/esphome/docker/.env.example](/Users/cyan/code/collect-server/deploy/esphome/docker/.env.example)

对应复制出的 `.env` 里设置：

```dotenv
HTTP_PROXY=http://10.30.5.172:7897
HTTPS_PROXY=http://10.30.5.172:7897
NO_PROXY=localhost,127.0.0.1,10.30.5.0/24
```

容器启动时会自动继承进去。

## 相关文件

- Compose 文件：[compose.yaml](/Users/cyan/code/collect-server/deploy/esphome/docker/compose.yaml)
- Docker 环境变量示例：[.env.example](/Users/cyan/code/collect-server/deploy/esphome/docker/.env.example)
- 首刷 runbook：[infohub-eink-first-flash-runbook.md](/Users/cyan/code/collect-server/docs/infohub-eink-first-flash-runbook.md)
- API 直连方案：[infohub-eink-direct-api-panel.md](/Users/cyan/code/collect-server/docs/infohub-eink-direct-api-panel.md)

## 参考资料

- ESPHome 官方命令行与 Docker 指南：
  [Getting Started with the Command Line and Docker](https://esphome.io/guides/getting_started_command_line/)
- Home Assistant 官方安装方式说明：
  [Home Assistant Installation](https://www.home-assistant.io/installation)
