# InfoHub 服务与采集器使用说明

这份文档从“怎么运行、怎么接入、怎么验证”的角度描述当前仓库里的服务。架构设计背景见根目录 [DESIGN.md](../../DESIGN.md)，电子墨水屏硬件链路见同目录下的 e-ink 专题文档。

## 1. 服务清单

| 名称 | 入口 | 作用 | 常见运行位置 |
|------|------|------|--------------|
| `infohub` | `cmd/infohub` | HTTP API、调度器、存储、仪表盘、collector 注册入口 | 服务器、Mac、局域网常驻机、容器 |
| `infohub-agent` | `cmd/infohub-agent` | 在开发机上扫描本机 Claude/Codex JSONL，并上报到 `infohub` | 每台开发机 |
| `claude_relay` collector | `internal/collector/claude_relay.go` | 采集 Claude Relay 账号今日用量和 5H/Week 额度 | `infohub` 进程内 |
| `sub2api` collector | `internal/collector/sub2api.go` | 采集 Sub2API 账号/用户今日用量和 Codex 5H/Week 额度 | `infohub` 进程内 |
| `feishu` collector | `internal/collector/feishu.go` | 从一个自定义飞书相关 JSON 端点读取任务列表或任务计数 | `infohub` 进程内 |
| `claude_local` collector | `internal/collector/local_usage.go` | 只读扫描本机 Claude Code 记录，或聚合 agent 上报记录 | `infohub` 进程内 |
| `codex_local` collector | `internal/collector/local_usage.go` | 只读扫描本机 Codex CLI 记录，或聚合 agent 上报记录 | `infohub` 进程内 |
| ESPHome/e-ink 设备 | `deploy/esphome` | 通过设备 JSON 接口把仪表盘画到电子纸屏 | reTerminal E1001 |

注意：collector 不是独立进程，它们都注册在 `infohub` 这个 Go 进程里。仓库里真正的两个可执行入口是 `infohub` 和 `infohub-agent`。

## 2. `infohub` 主服务

### 2.1 构建与启动

```bash
make build
./bin/infohub -config config.yaml

# 开发模式
make run
```

`-config` 默认是 `config.yaml`。配置加载时会先读取配置文件同目录下的 `.env`，再展开 `${VAR_NAME}`。

最小可运行配置通常需要：

| 变量 | 说明 |
|------|------|
| `INFOHUB_PORT` | HTTP 端口，默认 `8080` |
| `INFOHUB_AUTH_TOKEN` | 普通 API Bearer token；为空则普通 API 不鉴权 |
| `INFOHUB_DASHBOARD_TOKEN` | 仪表盘 URL 查询参数 token |
| `INFOHUB_INGEST_TOKEN` | agent 上报 token；为空时回退 `INFOHUB_AUTH_TOKEN` |
| `INFOHUB_STORE_TYPE` | `sqlite` 或 `memory`，默认 `sqlite` |
| `INFOHUB_SQLITE_PATH` | SQLite 文件路径，默认 `./data/infohub.db` |
| `INFOHUB_LOG_LEVEL` | `debug`、`info`、`warn`、`error` |

### 2.2 Docker 运行

```bash
docker build -t infohub .
docker run -p 8080:8080 \
  -e INFOHUB_AUTH_TOKEN=your-api-token \
  -e INFOHUB_DASHBOARD_TOKEN=your-dashboard-token \
  -e INFOHUB_STORE_TYPE=sqlite \
  -e INFOHUB_SQLITE_PATH=/data/infohub.db \
  -v "$PWD/data:/data" \
  infohub
```

如果容器里启用 `claude_local` 或 `codex_local` 的 `builtin` 模式，还需要挂载宿主机的 Claude/Codex 数据目录。服务器聚合多台开发机时，更推荐把本地 collector 设为 `mode: "remote"`，并在开发机上跑 `infohub-agent`。

### 2.3 API 验证

当 `INFOHUB_AUTH_TOKEN` 非空时，普通 API 需要 Bearer token：

```bash
curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/health

curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/summary
```

仪表盘可以用 Bearer token，也可以用 `dashboard_token` 查询参数：

```bash
curl "http://127.0.0.1:8080/dashboard/eink.json?token=$INFOHUB_DASHBOARD_TOKEN&refresh=300"
curl "http://127.0.0.1:8080/dashboard/eink/device.json?token=$INFOHUB_DASHBOARD_TOKEN&refresh=300"
```

手动触发某个 collector：

```bash
curl -X POST -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/collect/claude_local
```

collector 名称包括：`claude_relay`、`sub2api`、`feishu`、`claude_local`、`codex_local`。

## 3. `infohub-agent`

`infohub-agent` 用于服务端读不到开发机本地文件的场景。它在每台开发机上增量扫描 JSONL，只上传统计事件和额度观测，不上传 prompt 内容。

### 3.1 构建与配置

```bash
make build-agent
mkdir -p ~/.config/infohub-agent
cp deploy/agent/config.example.yaml ~/.config/infohub-agent/config.yaml
```

编辑 `~/.config/infohub-agent/config.yaml`：

```yaml
server:
  base_url: "https://your-infohub.example.com"
  ingest_token: "${INFOHUB_INGEST_TOKEN}"

machine_id: ""          # 留空时默认 hostname
interval_seconds: 120
```

如果配置文件旁边有 `.env`，agent 也会先读取它再展开 `${VAR_NAME}`。

### 3.2 运行模式

单次扫描并上报：

```bash
./bin/infohub-agent -once
```

常驻运行：

```bash
./bin/infohub-agent
```

常驻模式启动后会立即执行一次扫描和上报；之后按 `interval_seconds` 周期执行，默认 `120` 秒。

只在本机查看用量，不依赖服务器：

```bash
./bin/infohub-agent -print
./bin/infohub-agent -print -json
```

macOS 定时运行示例：

```bash
cp deploy/agent/com.infohub.agent.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/com.infohub.agent.plist
```

Linux/服务器上可用 Supervisor 常驻运行。这里不要带 `-once`，agent 会保持进程存活，并按 `interval_seconds` 每 120 秒扫描上报一次。

```bash
sudo cp bin/infohub-agent /usr/local/bin/
sudo mkdir -p /etc/infohub-agent /var/log/infohub-agent /var/lib/infohub-agent
sudo cp deploy/agent/config.example.yaml /etc/infohub-agent/config.yaml

# 先编辑 /etc/infohub-agent/config.yaml：server.base_url、ingest_token、paths
# 再编辑 supervisor 模板里的 user/HOME，并确保日志和状态目录归该用户
sudo cp deploy/agent/supervisor/infohub-agent.conf /etc/supervisor/conf.d/infohub-agent.conf
sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl status infohub-agent
```

Homebrew 安装的 Supervisor 通常读取 `/opt/homebrew/etc/supervisor.d/*.ini`，可复制同一个模板但目标文件名用 `.ini`：

```bash
cp deploy/agent/supervisor/infohub-agent.conf /opt/homebrew/etc/supervisor.d/infohub-agent.ini
supervisorctl reread
supervisorctl update
supervisorctl status infohub-agent
```

### 3.3 服务端 remote 模式

服务器端聚合 agent 数据时，`infohub` 建议使用 SQLite，并把本地 collector 设为 remote：

```yaml
store:
  type: "sqlite"

server:
  ingest_token: "${INFOHUB_INGEST_TOKEN}"

collectors:
  claude_local:
    enabled: true
    mode: "remote"
    online:
      enabled: false
  codex_local:
    enabled: true
    mode: "remote"
    online:
      enabled: false
```

不要在同一台机器上同时让 `infohub` 用 `builtin` 扫描本机目录，又让 `infohub-agent` 上报同一份目录，否则会双重计数。

## 4. 采集器配置

### 4.1 `claude_relay`

用途：采集 Claude Relay 的账号列表、今日 token 用量、请求数、成本、5H/Week 额度。

最小配置：

```yaml
collectors:
  claude_relay:
    enabled: true
    cron: "*/10 * * * *"
    timeout_seconds: 15
    service:
      base_url: "${CLAUDE_RELAY_BASE_URL}"
      endpoints:
        accounts: "/admin/claude-accounts"
        usage: "/admin/claude-accounts/usage"
    auth:
      type: "login_json"
      login_endpoint: "/web/auth/login"
      method: "POST"
      token_path: "token"
      credentials:
        username: "${CLAUDE_RELAY_USERNAME}"
        password: "${CLAUDE_RELAY_PASSWORD}"
```

输出要点：

| Category | 说明 |
|----------|------|
| `token_usage` | 今日 token、请求数、成本、启用账号列表 |
| `quota` | 每个账号的 5H/Week 剩余额度、重置时间 |

验证：

```bash
curl -X POST -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/collect/claude_relay
curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/source/claude_relay
```

常见问题：

| 现象 | 处理 |
|------|------|
| `service.base_url is empty` | 检查 `CLAUDE_RELAY_BASE_URL` 或配置文件 |
| `auth token not found` | 检查登录接口响应与 `auth.token_path` 是否匹配 |
| 上游 401/403 | 检查用户名、密码、登录端点；collector 会在 401/403 后尝试刷新 login token |

### 4.2 `sub2api`

用途：采集 Sub2API 的 OpenAI/Codex OAuth 账号额度、指定用户今日用量，以及该用户的 Anthropic 平台额度（面板映射为 DeepSeek）。

最小配置：

```yaml
collectors:
  sub2api:
    enabled: true
    cron: "*/10 * * * *"
    timeout_seconds: 15
    service:
      base_url: "${SUB2API_BASE_URL}"
      endpoints:
        accounts: "/api/v1/admin/accounts"
        today_stats: "/api/v1/admin/accounts/today-stats/batch"
        search_users: "/api/v1/admin/usage/search-users"
        usage_stats: "/api/v1/admin/usage/stats"
        user_platform_quotas: "/api/v1/admin/users/{id}/platform-quotas"
        user_detail: "/api/v1/admin/users/{id}"
    targets:
      - type: "user"
        email: "admin@example.com"
      - type: "account"
        match: "Pro 20x"
        include_usage: true
        include_quota: true
    auth:
      type: "login_json"
      login_endpoint: "${SUB2API_BASE_URL}/api/v1/auth/login"
      token_path: "data.access_token"
      credentials:
        email: "${SUB2API_ADMIN_EMAIL}"
        password: "${SUB2API_ADMIN_PASSWORD}"
```

`targets` 可选。为空时会采集所有 active OAuth 账号；配置后只采集匹配目标。

| 字段 | 说明 |
|------|------|
| `type: "user"` | 通过 `email`、`id` 或 `match` 查询用户今日用量 |
| `type: "account"` | 通过 `id`、`name` 或 `match` 匹配账号 |
| `include_usage` | 对账号额外请求今日用量 |
| `include_quota` | 配置语义字段；当前账号匹配后都会输出额度项 |

输出要点：

| Category | 说明 |
|----------|------|
| `token_usage` | 匹配账号/用户合计今日 token、请求数、成本 |
| `token_usage_user` | 指定用户今日 token、请求数、成本 |
| `token_usage_product` | 按上游端点拆分的 DeepSeek/Codex 今日 token、请求数、成本 |
| `product_quota` | DeepSeek 的日/周/月美元剩余额度；均未配置时回退用户余额 |
| `quota` | 每个账号的 Codex 5H/Week 剩余额度 |

验证：

```bash
curl -X POST -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/collect/sub2api
curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/source/sub2api
```

常见问题：

| 现象 | 处理 |
|------|------|
| `sub2api accounts payload missing data.items` | 上游账号接口响应结构和当前解析器不匹配 |
| `sub2api user ... not found` | 检查 `targets` 里的 email/match，或先用 Sub2API 后台确认用户存在 |
| 今日用量为 0 | 检查 `targets.include_usage`，以及账号 ID 是否被 `today_stats` 接口接受 |

### 4.3 `feishu`

用途：从一个飞书相关或自定义聚合端点读取任务数据。当前实现不是完整飞书 OpenAPI 客户端，它只会请求 `base_url + endpoint`，并尝试从 JSON 中解析 `tasks`、`work_items`、`issues`、`records`、`list` 或任务计数字段。

最小配置：

```yaml
collectors:
  feishu:
    enabled: true
    cron: "*/30 * * * *"
    base_url: "https://your-adapter.example.com"
    endpoint: "/api/tasks"
    project_key: "your-project"
    timeout_seconds: 15
    headers:
      Authorization: "Bearer ${FEISHU_ADAPTER_TOKEN}"
```

支持的响应形态示例：

```json
{
  "tasks": [
    { "title": "接入新 collector", "status": "doing" },
    { "title": "补充文档", "assignee": "cyan" }
  ]
}
```

或：

```json
{ "count": 3 }
```

输出要点：

| Category | 说明 |
|----------|------|
| `tasks` | 活跃任务数和任务条目 |

常见问题：

| 现象 | 处理 |
|------|------|
| `feishu endpoint is empty` | 配置 `collectors.feishu.endpoint` |
| `cannot parse feishu task payload` | 上游 JSON 中没有当前支持的任务数组或计数字段 |
| 需要真实飞书鉴权 | 建议先做一个适配器端点，InfoHub 当前只负责读取 JSON |

### 4.4 `claude_local`

用途：统计 Claude Code 本机用量。支持三种模式：

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| `builtin` | InfoHub 直接递归扫描 JSONL | InfoHub 跑在同一台开发机上 |
| `ccusage` | 调用 `npx ccusage@latest --json`，失败后回退 builtin | 需要复用 ccusage 解析结果 |
| `remote` | 不扫本机文件，只聚合 agent 上报记录 | InfoHub 跑在服务器上 |

本机模式配置：

```yaml
collectors:
  claude_local:
    enabled: true
    cron: "*/5 * * * *"
    paths:
      - "${HOME}/.config/claude/projects"
      - "${HOME}/.claude/projects"
    mode: "builtin"
    online:
      enabled: true
    quota:
      plan: "local"
      five_hour_msg_cap: 0
      weekly_msg_cap: 0
```

`online.enabled: true` 时会只读本机 Claude Code OAuth 凭据查询官方用量接口；不会写回 Claude Code 配置。

输出要点：

| Category | 说明 |
|----------|------|
| `token_usage` | 今日 token、消息数、模型分布、按模型价格估算的 `daily_cost` |
| `quota` | 5H/今日/Week 维度用量或剩余额度 |
| `usage` | Top 模型、Top 模型成本、cache hit 等辅助展示项 |

本地用量成本在服务端聚合阶段估算：按上报记录里的模型名匹配内置价格表，再分别用普通输入、缓存输入、缓存写入、输出、reasoning token 计算美元成本。未识别模型不会套用默认价格，会计入 `unpriced_tokens`，避免误报成本。价格表参考官方 OpenAI API Pricing 与 Anthropic Claude API Pricing；如供应商调价，需要同步更新 `internal/collector/local_usage_pricing.go`。

常见问题：

| 现象 | 处理 |
|------|------|
| 路径缺失 | 检查 `paths` 是否匹配当前 Claude Code 数据目录 |
| 额度未知 | 检查本机 Keychain 或 `${HOME}/.claude/.credentials.json` 是否存在有效 OAuth 凭据 |
| 服务器上扫不到数据 | 改用 `mode: "remote"` 并部署 `infohub-agent` |

更多设计细节见 [本地 Claude/Codex 用量采集](./infohub-local-claude-codex-usage.md)。

### 4.5 `codex_local`

用途：统计 Codex CLI 本机用量。支持 `builtin` 与 `remote`：

```yaml
collectors:
  codex_local:
    enabled: true
    cron: "*/5 * * * *"
    paths:
      - "${HOME}/.codex/sessions"
    mode: "builtin"
    online:
      enabled: true
      # auth_path: "${HOME}/.codex/auth.json"
      # base_url: "https://chatgpt.com"
    quota:
      plan: "local"
      five_hour_msg_cap: 0
      weekly_msg_cap: 0
```

`online.enabled: true` 时会读取 Codex CLI 的 `auth.json` 查询在线 5H/Week 额度兜底。

输出要点和 `claude_local` 类似，source 名称为 `codex_local`。如果 Codex 配额已内嵌在 JSONL 记录里，collector 会优先使用记录里的观测；记录过期或机器闲置时再尝试在线兜底。

常见问题：

| 现象 | 处理 |
|------|------|
| 今日用量为空 | 检查 `${HOME}/.codex/sessions` 下是否有当日 JSONL |
| 在线额度失败 | 检查 `auth_path` 是否存在、token 是否过期、网络是否可达 `base_url` |
| 多机器汇总 | 服务端用 `mode: "remote"`，每台开发机跑 `infohub-agent` |

## 5. 仪表盘与 ESPHome

InfoHub 提供三个仪表盘端点：

| 端点 | 用途 |
|------|------|
| `/dashboard/eink` | HTML 仪表盘，适合浏览器或截图 |
| `/dashboard/eink.json` | 调试 JSON，适合排查聚合结果 |
| `/dashboard/eink/device.json` | ESPHome 直连 JSON，适合设备端解析绘制 |

常用 URL：

```text
http://<infohub-host>:8080/dashboard/eink?token=<DASHBOARD_TOKEN>&refresh=600
http://<infohub-host>:8080/dashboard/eink.json?token=<DASHBOARD_TOKEN>&refresh=300
http://<infohub-host>:8080/dashboard/eink/device.json?token=<DASHBOARD_TOKEN>&refresh=300
```

仪表盘默认读取 `dashboard.sources.sub2api`，并把同一用户的今日消耗按上游端点拆成 DeepSeek 与 Codex：

```yaml
dashboard:
  sources:
    sub2api: "sub2api"
```

`/v1/messages` 归为 DeepSeek，`/v1/responses` 归为 Codex。面板同时展示两者的 Token、请求、成本，DeepSeek 的 Anthropic 平台美元额度（没有周期限额时显示用户余额），以及 Codex OAuth 账号的 5H/Week 剩余额度。

ESPHome 设备配置入口在 `deploy/esphome`。首刷、Docker 编译、设备直连和局部刷新文档见：

- [首次刷机指南](./infohub-eink-first-flash-runbook.md)
- [直连 API 面板](./infohub-eink-direct-api-panel.md)
- [部署与显示调优](./infohub-eink-deploy-and-display-tuning.md)
- [macOS 上的 ESPHome Docker](./infohub-eink-esphome-docker-mac.md)
- [局部刷新探测](./infohub-eink-partial-refresh-probe.md)

## 6. 日常排障顺序

1. 看进程是否启动：`make run` 或 `./bin/infohub -config config.yaml`。
2. 看配置是否展开正确：确认 `.env` 和配置文件在同一目录，环境变量没有空值。
3. 看 collector 状态：

```bash
curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/health
```

4. 手动触发单个 collector：

```bash
curl -X POST -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/collect/<collector-name>
```

5. 看单个 source 快照：

```bash
curl -H "Authorization: Bearer $INFOHUB_AUTH_TOKEN" \
  http://127.0.0.1:8080/api/v1/source/<collector-name>
```

6. 仪表盘不显示时，先看 `/dashboard/eink.json`，再看 `/dashboard/eink/device.json`。
7. agent 上报链路不通时，先在开发机跑 `infohub-agent -print` 确认本机可扫描，再跑 `infohub-agent -once` 确认服务端 ingest token 和 URL。
