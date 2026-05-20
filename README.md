# proxy_doubao

`proxy_doubao` 是一个基于 Go 标准库实现的本地 API 代理，用来把 OpenAI Codex 的 `/v1/responses` 请求转换为火山方舟（Volcano Engine Ark）兼容的 `/chat/completions` 请求，并将上游 SSE 流实时转换回 Codex 期望的 `/v1/responses` 事件序列。

Codex 只需要把 API Base URL 指向 `http://localhost:8080/v1`，即可通过本地代理透明使用方舟模型。

## 功能特性

- 兼容 Codex `/v1/responses` 到上游 `/chat/completions` 的双向协议转换
- 支持普通响应和 `text/event-stream` 流式响应
- 支持 `input`、`instructions`、`function_call`、`function_call_output` 等 Responses 字段
- 自动将 `developer` 角色映射为上游兼容的 `system`
- 自动规范化 `tools`，过滤不兼容或无效的 function tool
- 支持多提供者配置和 `PRIMARY_PROVIDER` 主提供者切换
- 支持 `FORCE_MODEL_OVERRIDE=1` 强制覆盖请求中的模型名
- 流式响应显式输出 `charset=utf-8`，降低 Windows 终端乱码风险
- 流式请求不受总超时截断，适合长时间工具调用或大块 SSE 输出

## 工作原理

```text
┌──────────┐     POST /v1/responses     ┌──────────┐     POST /chat/completions     ┌──────────┐
│  Codex   │ ─────────────────────────▶ │   代理   │ ─────────────────────────────▶ │  方舟    │
│ (客户端) │ ◀───────────────────────── │ (本项目) │ ◀───────────────────────────── │ (上游)   │
└──────────┘     SSE /v1/responses      └──────────┘     SSE /chat/completions       └──────────┘
```

数据流分为两段：

1. 请求方向：Codex `POST /v1/responses` -> 代理转换请求体 -> 上游 `POST /chat/completions`
2. 响应方向：上游 SSE 或普通 JSON -> 代理转换为 `/v1/responses` 兼容格式 -> 返回给 Codex

## 快速开始

### 前置条件

- Go 1.25+
- 可用的火山方舟 API Key

### 1. 克隆并进入项目

```bash
git clone <repo-url>
cd proxy_doubao
```

### 2. 准备配置文件

Windows PowerShell:

```powershell
Copy-Item env.backup .env
```

Linux/macOS:

```bash
cp env.backup .env
```

然后编辑 `.env`，填入你自己的 API Key 和模型配置。

### 3. 启动服务

直接运行：

```bash
go run .
```

或先构建再运行：

```bash
go build -o proxy_doubao.exe .
./proxy_doubao.exe
```

服务默认监听 `:8080`。

### 4. 在 Codex 中指向本地代理

把 Codex 的 API Base URL 配成：

```text
http://localhost:8080/v1
```

如果你的客户端需要环境变量，可以使用：

Linux/macOS:

```bash
export OPENAI_BASE_URL=http://localhost:8080/v1
export OPENAI_API_KEY=dummy
```

Windows PowerShell:

```powershell
$env:OPENAI_BASE_URL = "http://localhost:8080/v1"
$env:OPENAI_API_KEY = "dummy"
```

`OPENAI_API_KEY` 在这里可以是任意非空值，代理会替换成 `.env` 中配置的上游 API Key。

## 配置说明

配置来源支持 `.env` 和环境变量。优先级如下：

1. `PRIMARY_PROVIDER` 指定的提供者
2. 旧版 `VOLCANO_*` / `ARK_*`
3. 代码内默认值

### 推荐：多提供者配置

```env
# 当前主提供者，可选值需与下方 PROVIDER_<NAME>_* 中的 NAME 对应
PRIMARY_PROVIDER=ark_plan

# ark_v3：OpenAI 兼容 v3 接口
PROVIDER_ARK_V3_BASE_URL=https://ark.cn-beijing.volces.com/api/v3
PROVIDER_ARK_V3_API_KEY=your-api-key
PROVIDER_ARK_V3_MODEL=doubao-seed-2-0-code-preview-260215

# ark_plan：Plan v3 接口
PROVIDER_ARK_PLAN_BASE_URL=https://ark.cn-beijing.volces.com/api/plan/v3
PROVIDER_ARK_PLAN_API_KEY=your-api-key
PROVIDER_ARK_PLAN_MODEL=glm-5.1

# 强制用当前提供者的模型覆盖请求中自带的 model
FORCE_MODEL_OVERRIDE=1

# 基础调试日志
DEBUG_PROXY=0

# 详细调试日志：请求体、响应体、SSE 输入输出逐条打印
DEBUG_PROXY_VERBOSE=0
```

命名约定为：

```text
PROVIDER_<NAME>_BASE_URL
PROVIDER_<NAME>_API_KEY
PROVIDER_<NAME>_MODEL
```

其中 `<NAME>` 会被规范化为大写加下划线格式，例如 `ark-plan` 会映射为 `ARK_PLAN`。

### 兼容旧版环境变量

```env
VOLCANO_BASE_URL=https://ark.cn-beijing.volces.com/api/plan/v3
VOLCANO_API_KEY=your-api-key
VOLCANO_MODEL=glm-5.1
```

同样兼容：

- `ARK_BASE_URL`
- `ARK_API_KEY`
- `ARK_MODEL`

### 环境变量一览

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PRIMARY_PROVIDER` | 空 | 主提供者名称，设置后优先使用 `PROVIDER_<NAME>_*` |
| `PORT` | `8080` | 监听端口，`8080` 会自动规范化为 `:8080` |
| `UPSTREAM_TIMEOUT` | `60s` | 非流式上游请求超时；流式请求自动忽略总超时 |
| `FORCE_MODEL_OVERRIDE` | `0` | 强制使用当前配置里的 `model` 覆盖请求中的模型名 |
| `DEBUG_PROXY` | `0` | 输出请求摘要、上游状态、流开始/结束等基础调试日志 |
| `DEBUG_PROXY_VERBOSE` | `0` | 额外输出原始请求体、转换后请求体、SSE 输入和 SSE 输出 |

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/health` | 健康检查，返回 `{"status":"ok"}` |
| `POST` | `/v1/responses` | Codex 请求入口，代理会转发到上游 `/chat/completions` |

## 请求转换规则

代理会将 Codex `/v1/responses` 请求自动翻译为 OpenAI `/chat/completions` 请求。

### 主要字段映射

| Codex 字段 | 转换结果 |
|------------|----------|
| `instructions` | 追加为一条 `system` message |
| `input` 字符串 | 转为一条 `user` message |
| `input` 数组 | 按条目类型分别转换为 `user` / `assistant` / `tool` message |
| `input[].type=function_call` | 转为 `assistant.tool_calls` |
| `input[].type=function_call_output` | 转为 `tool` message |
| `role=developer` | 映射为 `role=system` |
| `max_output_tokens` | 映射为 `max_tokens` |
| 缺失或空的 `model` | 回退为配置中的默认模型 |
| `FORCE_MODEL_OVERRIDE=1` | 总是使用配置中的模型 |

### Tools 兼容规则

| 输入情况 | 处理方式 |
|----------|----------|
| `type=function` 且存在合法 `name` | 保留 |
| 顶层 `name` / `description` / `parameters` | 自动移入 `function` 块 |
| 非 `function` 类型 tool | 丢弃 |
| 缺少 `function.name` 的 tool | 丢弃 |
| 最终无可用 tool | 同时移除 `tools` 和 `tool_choice` |

## 流式响应转换

上游返回 `text/event-stream` 时，代理会将 `chat completion chunk` 转换为 Codex 可识别的 `/v1/responses` 事件流。

### 文本输出生命周期

```text
response.created
-> response.output_item.added
-> response.content_part.added
-> response.output_text.delta
-> response.content_part.done
-> response.output_text.done
-> response.output_item.done
-> response.completed
```

### 工具调用生命周期

```text
response.output_item.added (type=function_call)
-> response.function_call_arguments.delta
-> response.function_call_arguments.done
-> response.output_item.done
```

### 当前实现保证

- 文本输出和工具调用拥有稳定且唯一的 `output_index`
- `response.completed.output` 会按真实 `output_index` 排序汇总
- 流式响应头显式设置为 `text/event-stream; charset=utf-8`
- 大块单帧 SSE 数据不会再被 `bufio.Scanner` 的 token 上限截断
- 流式请求不会因为 `http.Client.Timeout` 提前中断

## 调试排查

常用调试开关：

```env
DEBUG_PROXY=1
DEBUG_PROXY_VERBOSE=1
```

打开后可以看到：

- 请求摘要
- 转换前请求体
- 转换后请求体
- 上游状态码和响应摘要
- 上游 SSE 输入逐条日志
- 代理 SSE 输出逐条日志

这对排查以下问题最有帮助：

- `messages` / `tools.function` 参数不兼容
- `developer` 角色未被正确映射
- `response.completed` 丢失
- 工具调用卡在 `function_call_arguments.delta`
- 上游模型名或基地址配置错误

## 开发与验证

```bash
# 运行全部测试
go test ./...

# 运行代理包测试
go test ./internal/proxy/

# 运行单个测试
go test ./internal/proxy/ -run TestProxyTransformsResponsesInputToMessages

# 构建
go build -o proxy_doubao.exe .
```

测试约定：

- 使用 `httptest.NewServer` 模拟上游
- 通过 `t.Setenv` 隔离配置测试
- 流式测试覆盖文本输出、工具调用、超时处理和大块 SSE 数据场景

## 项目结构

```text
main.go                          入口：加载配置 -> 创建代理 -> 启动 HTTP 服务
internal/
  config/config.go               多提供者配置加载，环境变量优先级链
  proxy/
    proxy.go                     核心代理：请求转发、流式/非流式处理、超时分支
    transform.go                 请求体翻译：Codex /v1/responses -> /chat/completions
    stream.go                    SSE 流翻译：上游 chunk -> Codex 事件序列
    debug.go                     调试日志摘要
  server/server.go               路由注册 + 请求日志中间件
  util/
    util.go                      字符串和 JSON 工具函数
    header.go                    HTTP 头复制（过滤 Authorization 和逐跳头）
```

## 许可证

私有项目，未授权禁止使用。
