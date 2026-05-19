# proxy_doubao

将 OpenAI Codex 的 `/v1/responses` 请求实时转换为火山方舟（Volcano Engine Ark）的 `/chat/completions` 请求，并双向转换 SSE 流式响应。Codex 配置 `http://localhost:8080/v1` 作为 API Base URL 即可透明使用方舟模型。

## 工作原理

```
┌──────────┐     POST /v1/responses     ┌──────────┐     POST /chat/completions     ┌──────────┐
│  Codex   │ ──────────────────────────▶ │   代理   │ ─────────────────────────────▶ │  方舟    │
│ (客户端)  │ ◀────────────────────────── │ (本项目)  │ ◀───────────────────────────── │ (上游)   │
└──────────┘     SSE /v1/responses      └──────────┘     SSE /chat/completions       └──────────┘
```

代理在两个不兼容的 API 之间做双向格式转换：

- **请求方向**：将 Codex 的 `input`/`instructions`/`function_call` 等字段翻译为 OpenAI `messages` 数组，将 `developer` 角色映射为 `system`，规范化 `tools` 格式
- **响应方向**：将上游的 `chat completion chunk` 流实时转换为 Codex `/v1/responses` 格式的 SSE 事件序列（`response.created` → `response.output_text.delta` → `response.completed` 等）

## 快速开始

### 前置条件

- Go 1.25+
- 火山方舟 API Key

### 安装与运行

```bash
# 克隆仓库
git clone <repo-url>
cd proxy_doubao

# 复制并编辑配置
cp env.backup .env
# 编辑 .env，填入你的 API Key

# 构建并运行
go build -o proxy_doubao.exe .
./proxy_doubao.exe

# 或直接运行
go run .
```

启动后服务监听 `:8080`，在 Codex 中将 API Base URL 设置为 `http://localhost:8080/v1` 即可。

### 配置 Codex

```bash
# 设置环境变量指向本地代理
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=任意值  # 代理会替换为方舟 API Key
```

## 配置

通过 `.env` 文件或环境变量配置。优先级：`PRIMARY_PROVIDER` 指定的提供者 > 旧版 `VOLCANO_*`/`ARK_*` 变量 > 默认值。

### 多提供者配置（推荐）

```env
# 选择主提供者
PRIMARY_PROVIDER=ark_plan

# 方舟 V3 提供者
PROVIDER_ARK_V3_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
PROVIDER_ARK_V3_API_KEY=your-api-key
PROVIDER_ARK_V3_MODEL=doubao-seed-2-0-code-preview-260215

# 方舟 Plan 提供者
PROVIDER_ARK_PLAN_BASE_URL=https://ark.cn-beijing.volces.com/api/plan/v3
PROVIDER_ARK_PLAN_API_KEY=your-api-key
PROVIDER_ARK_PLAN_MODEL=glm-5.1
```

命名约定：`PROVIDER_<NAME>_BASE_URL`、`PROVIDER_<NAME>_API_KEY`、`PROVIDER_<NAME>_MODEL`，其中 NAME 大写+下划线格式。

### 旧版环境变量

```env
VOLCANO_BASE_URL=https://ark.cn-beijing.volces.com/api/plan/v3
VOLCANO_API_KEY=your-api-key
VOLCANO_MODEL=glm-5.1
```

也兼容 `ARK_BASE_URL`、`ARK_API_KEY`、`ARK_MODEL`。

### 全部环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PRIMARY_PROVIDER` | 空 | 主提供者名称，设置后忽略旧版变量 |
| `PORT` | `8080` | 监听端口 |
| `UPSTREAM_TIMEOUT` | `60s` | 上游请求超时（流式请求自动忽略） |
| `FORCE_MODEL_OVERRIDE` | `0` | 强制用配置的 model 覆盖请求中的 model |
| `DEBUG_PROXY` | `0` | 启用调试摘要日志 |
| `DEBUG_PROXY_VERBOSE` | `0` | 输出完整请求/响应体 |

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/health` | 健康检查，返回 `{"status":"ok"}` |
| `POST` | `/v1/responses` | Codex 请求入口，代理转发到上游 `/chat/completions` |

## 请求转换规则

代理将 Codex `/v1/responses` 格式自动翻译为 OpenAI `/chat/completions` 格式：

| Codex 字段 | 翻译结果 |
|------------|----------|
| `instructions` | → `system` role message |
| `input`（字符串） | → `user` role message |
| `input`（数组） | → 按条目类型分派转换 |
| `input[].type=function_call` | → `assistant` tool_calls 消息 |
| `input[].type=function_call_output` | → `tool` role 消息 |
| `role=developer` | → `role=system` |
| `max_output_tokens` | → `max_tokens` |
| `tools[].name`（顶层） | → 移入 `tools[].function.name` |
| 非 `function` 类型 tools | → 丢弃 |

## 开发

```bash
# 运行全部测试
go test ./...

# 运行单个包的测试
go test ./internal/proxy/

# 运行单个测试
go test ./internal/proxy/ -run TestProxyTransformsResponsesInputToMessages

# 构建
go build -o proxy_doubao.exe .
```

## 项目结构

```
main.go                          入口：加载配置 → 创建代理 → 启动 HTTP 服务
internal/
  config/config.go               多提供者配置加载，环境变量优先级链
  proxy/
    proxy.go                     核心代理：请求转发、流式/非流式分支、超时处理
    transform.go                 请求体翻译：Codex → OpenAI 格式
    stream.go                    响应流翻译：上游 chunk → Codex SSE 事件序列
    debug.go                     调试日志摘要
  server/server.go               路由注册 + 请求日志中间件
  util/
    util.go                      字符串/JSON 工具函数
    header.go                    HTTP 头复制（过滤 Authorization 和逐跳头）
```

## 许可证

私有项目，未授权禁止使用。
