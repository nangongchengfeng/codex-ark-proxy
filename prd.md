### 1. 项目背景与目标

#### 1.1 背景

- 新版 OpenAI Codex（CLI/IDE 插件）强制要求后端提供 `/v1/responses` 接口，用于处理对话与工具调用。
- 实际推理服务部署在火山方舟，地址为 `https://ark.cn-beijing.volces.com/api/plan/v3`，其 OpenAI 兼容接口仅为 `/chat/completions`，不支持 `/v1/responses` 端点。
- 需要一个轻量级中间层，将 Codex 的 `/v1/responses` 请求转换为火山方舟的 `/chat/completions` 请求，并正确处理流式响应，让 Codex 能够透明使用火山方舟的 GLM-5.1 模型。

#### 1.2 目标

- 提供本地 HTTP 服务（端口 8080），Codex 仅需配置 `http://localhost:8080/v1` 作为 API Base URL。
- 准确转换请求路径、请求体、请求头，实现无缝对接。
- 支持流式（Server-Sent Events）响应实时回传，保持低延迟。
- 易于配置和部署，支持通过环境变量指定火山方舟地址、API Key、模型名等。

------

### 2. 用户故事与验收标准

#### 2.1 用户故事

- **作为** 使用 Codex 的开发者，
  **我想要** 在 Codex 配置中填入 `http://localhost:8080/v1`，
  **以便** 通过火山方舟的 GLM-5.1 模型完成代码补全/对话，而无需关心底层 API 差异。

#### 2.2 验收标准

1. 当 Codex 发送 `POST /v1/responses` 请求时，中间层能够返回正确的流式响应，且内容来自火山方舟。
2. 流式响应过程中，Codex 可以逐步接收 token 并显示，无截断或卡顿。
3. 中间层能够将火山方舟的 SSE 事件转换为符合 `/v1/responses` 格式的流式数据（若格式不一致则保持原样转发，并确认 Codex 可解析）。
4. 所有环境相关配置（目标 URL、API Key、模型名）均可通过环境变量覆盖，不硬编码。
5. 提供基础健康检查端点 `GET /health`，返回 200 OK。
6. 出现后端错误（如 4xx/5xx）时，中间层将错误状态码和消息透明返回给 Codex。

------

### 3. 功能需求

#### 3.1 请求路由与转发

- **监听路径**：`POST /v1/responses`
- **转发目标**：`POST {VOLCANO_BASE_URL}/chat/completions`
  （`VOLCANO_BASE_URL` 默认为 `https://ark.cn-beijing.volces.com/api/plan/v3`，支持环境变量覆盖）
- **请求体转换**：
  - 将 Codex 发来的 JSON 请求体（含 `model`, `messages`, `stream: true` 等）直接透传，不修改字段名，因为火山方舟 `/chat/completions` 的请求格式与 OpenAI Chat Completions API 兼容。
  - 若 Codex 请求中包含 `/v1/responses` 特有字段（如 `tools`, `tool_choice`, `instructions` 等），且火山方舟接口支持类似 `tools`/`function calling`，则直接透传；若不支持，中间层不做特殊转换，由火山方舟返回错误，后期按需适配。
- **请求头处理**：
  - 复制 Codex 发来的 `Content-Type`、`Accept` 等头。
  - **强制添加/覆盖**：
    - `Authorization: Bearer {VOLCANO_API_KEY}` （从环境变量读取）
    - `Content-Type: application/json`（若缺失）
  - 移除原请求中的 `Authorization` 头（避免混淆），不再转发给火山方舟。

#### 3.2 流式响应处理

- 火山方舟在 `stream: true` 时返回 `text/event-stream`（SSE 格式，每行 `data: {json}\n\n`）。
- 中间层必须将火山方舟的响应以“流式”方式写回给 Codex，不能等全部接收完再返回。
- **实现方式**：
  - 中间层开启向火山方舟的请求（使用 Go 的 `http.Client`，不缓冲整个响应体）。
  - 读取火山方舟的 `Response.Body`，逐行扫描 SSE 事件，立即写入当前客户端连接的 `ResponseWriter`，并定期调用 `Flusher.Flush()` 推送数据。
  - 火山方舟返回的 SSE 事件可以原样写入，因为新版 Codex 的 `/v1/responses` 流式响应同样使用 SSE 格式，不需要转换格式。
  - 注意转发结束标记：当后端流结束时，中间层应正常关闭连接。

#### 3.3 非流式请求

- 如果 Codex 发送非流式请求（`stream: false` 或未设置），中间层同样转发给火山方舟，将完整 JSON 响应一次性返回，保持 Content-Type 为 `application/json`。

#### 3.4 健康检查

- 提供 `GET /health` 端点，返回 `{"status":"ok"}` 和 200 状态码，供容器健康探针或调试使用。

#### 3.5 日志与错误处理

- 记录每次请求的方法、路径、耗时、后端状态码，便于调试。
- 后端超时或连接失败时，向客户端返回 502 Bad Gateway 及简洁的错误信息，不暴露内部细节。
- 客户端断开连接时，应取消对后端的请求（使用 `context.Context` 取消机制），避免资源浪费。