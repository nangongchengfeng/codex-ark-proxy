# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Go 实现的 API 代理，将 OpenAI Codex 的 `/v1/responses` 请求转换为火山方舟（Volcano Engine Ark）的 `/chat/completions` 请求，并实时转换 SSE 流式响应。Codex 配置 `http://localhost:8080/v1` 作为 API Base URL 即可透明使用。

## 构建与验证

```bash
# 构建
go build -o proxy_doubao.exe .

# 运行测试
go test ./...

# 运行单个包的测试
go test ./internal/proxy/

# 运行单个测试（用 -run 指定测试名）
go test ./internal/proxy/ -run TestProxyTransformsResponsesInputToMessages

# 启动服务（需要 .env 中配置 API Key）
go run .
```

## 架构

```
main.go                          → 入口：加载配置 → 创建 Proxy → 启动 HTTP Server
internal/config/config.go        → 多提供者配置加载，环境变量优先级链
internal/proxy/proxy.go          → 核心代理：请求转发、流式/非流式分支、超时处理
internal/proxy/transform.go      → 请求体转换：Codex /v1/responses → OpenAI /chat/completions
internal/proxy/stream.go         → SSE 流转换：上游 chunk → /v1/responses 事件序列
internal/proxy/debug.go          → 调试日志摘要
internal/server/server.go        → 路由注册 + 请求日志中间件
internal/util/util.go            → 通用字符串/JSON 工具函数
internal/util/header.go          → HTTP 头复制（过滤 Authorization 和逐跳头）
```

### 核心数据流

1. **请求方向**：Codex `POST /v1/responses` → `transformRequestPayload` 转换请求体 → `POST {BaseURL}/chat/completions`
2. **响应方向**：上游 SSE 流 → `streamResponse` 逐 chunk 解析 → `responseStreamState` 维护状态机 → 输出 `/v1/responses` 格式的 SSE 事件序列

### 请求体转换关键规则（transform.go）

- `input` 字段 → `messages` 数组（支持 string、数组、嵌套 input item）
- `instructions` 字段 → system role message
- `input` 中的 `function_call` → assistant tool_calls 消息
- `input` 中的 `function_call_output` → tool role 消息
- `developer` role → `system` role
- `max_output_tokens` → `max_tokens`
- tools 只保留 `type=function`，顶层 `name`/`description`/`parameters` 移入 `function` 块
- `FORCE_MODEL_OVERRIDE=1` 时强制覆盖 model 字段

### SSE 流转换状态机（stream.go）

`responseStreamState` 管理完整的 `/v1/responses` 事件生命周期：
`response.created` → `response.output_item.added` → `response.content_part.added` → `response.output_text.delta`(多次) → `response.content_part.done` → `response.output_text.done` → `response.output_item.done` → `response.completed`

工具调用走独立分支：`response.output_item.added(type=function_call)` → `response.function_call_arguments.delta`(多次) → `response.function_call_arguments.done` → `response.output_item.done`

## 配置

通过 `.env` 文件或环境变量配置。优先级：`PRIMARY_PROVIDER` 指定的提供者 > 旧版 `VOLCANO_*`/`ARK_*` 变量 > 默认值。

多提供者命名约定：`PROVIDER_<NAME>_BASE_URL`、`PROVIDER_<NAME>_API_KEY`、`PROVIDER_<NAME>_MODEL`（NAME 大写+下划线格式）。

调试开关：`DEBUG_PROXY=1` 启用摘要日志，`DEBUG_PROXY_VERBOSE=1` 输出完整请求/响应体。

## 测试约定

- 测试使用 `httptest.NewServer` 模拟上游，验证请求转换和流式转换正确性
- 配置测试通过 `t.Setenv` 隔离环境变量
- 需要手动清空所有 `PROVIDER_*` 变量以避免 `.env` 文件干扰默认值测试
