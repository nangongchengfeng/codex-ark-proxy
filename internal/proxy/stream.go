package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"proxy_doubao/internal/util"
)

// chatCompletionChunk 表示上游 /chat/completions 流式响应中的一个 SSE chunk。
type chatCompletionChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// responseStreamState 管理 Codex /v1/responses SSE 流的完整生命周期状态。
type responseStreamState struct {
	responseID string
	model      string
	createdAt  int64

	textItemID    string
	textOutputIdx int
	nextOutputIdx int
	textBuilder   strings.Builder
	createdSent   bool
	itemSent      bool
	partSent      bool

	toolCalls    map[int]*toolCallState
	debugVerbose bool
}

// toolCallState 跟踪单个工具调用的流式状态。
type toolCallState struct {
	index     int
	outputIdx int
	itemID    string
	callID    string
	name      string
	args      strings.Builder
	itemSent  bool
}

// streamResponse 将上游 SSE 流实时转换为 Codex /v1/responses 格式。
func streamResponse(w io.Writer, body io.Reader, flusher http.Flusher, debug bool, verbose bool) error {
	// 逐行读取上游 SSE 事件流，避免 Scanner 的 token 长度限制截断大 chunk。
	reader := bufio.NewReaderSize(body, 64*1024)

	// 初始化流状态机（跟踪 response 生命周期 + 文本/工具调用状态）

	state := &responseStreamState{debugVerbose: verbose}
	chunkCount := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			if debug {
				log.Printf("[proxy-debug] stream-scan-error chunks=%d err=%v", chunkCount, err)
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if err == io.EOF && line == "" {
			break
		}
		// 跳过非 SSE data 行
		if !strings.HasPrefix(line, "data: ") {
			if err == io.EOF {
				break
			}
			continue
		}

		// 提取 data: 后的 JSON payload

		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		// 跳过空 data 行
		if data == "" {
			continue
		}
		// ★ 上游流结束标记，跳出扫描循环
		if data == "[DONE]" {
			if debug {
				log.Printf("[proxy-debug] stream-done-marker chunks=%d", chunkCount)
			}
			if verbose {
				log.Printf("[proxy-debug-sse-in] chunk=%d data=%s", chunkCount+1, data)
			}
			break
		}
		if verbose {
			log.Printf("[proxy-debug-sse-in] chunk=%d data=%s", chunkCount+1, data)
		}

		// 解析上游 chunk JSON

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			if debug {
				log.Printf("[proxy-debug] stream-skip-invalid-json len=%d", len(data))
			}
			continue
		}
		chunkCount++

		// ★ 核心：将上游 chunk 转换为 Codex SSE 事件并写入客户端

		if err := state.consumeChunk(chunk, w, flusher); err != nil {
			if debug {
				log.Printf("[proxy-debug] stream-consume-error chunks=%d err=%v", chunkCount, err)
			}
			return err
		}
		if err == io.EOF {
			break
		}
	}

	// 发送流结束事件：content_part.done → output_text.done → output_item.done → response.completed

	if err := state.finish(w, flusher); err != nil {
		if debug {
			log.Printf("[proxy-debug] stream-finish-error chunks=%d err=%v", chunkCount, err)
		}
		return err
	}
	if debug {
		log.Printf("[proxy-debug] stream-finish-ok chunks=%d created=%t text_item=%t tool_calls=%d", chunkCount, state.createdSent, state.itemSent, len(state.toolCalls))
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		if debug {
			log.Printf("[proxy-debug] stream-write-done-error err=%v", err)
		}
		return err
	}
	flusher.Flush()
	return nil
}

func (s *responseStreamState) consumeChunk(chunk chatCompletionChunk, w io.Writer, flusher http.Flusher) error {
	// ★ 延迟初始化：收到第一个有效 chunk 时才构建 response ID / model 等元信息
	if s.responseID == "" {
		s.responseID = util.FirstNonEmpty(strings.TrimSpace(chunk.ID), "resp-proxy")
		s.textItemID = "msg-" + s.responseID
		s.textOutputIdx = -1
		s.model = strings.TrimSpace(chunk.Model)
		s.createdAt = chunk.Created
		s.toolCalls = map[int]*toolCallState{}
	}

	// 发送 response.created 事件（整个流仅发送一次）

	// 如果从未发送过 created 事件（无有效 chunk），直接返回

	if !s.createdSent {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":         s.responseID,
				"object":     "response",
				"created_at": s.createdAt,
				"model":      s.model,
				"status":     "in_progress",
				"output":     []any{},
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		s.createdSent = true
	}

	// 遍历 choices：处理工具调用增量和文本增量

	for _, choice := range chunk.Choices {
		// 处理 tool_calls 增量（function_call_arguments.delta 事件）
		for _, toolCall := range choice.Delta.ToolCalls {
			if err := s.consumeToolCall(toolCall, w, flusher); err != nil {
				return err
			}
		}

		// 处理文本增量（output_text.delta 事件）

		if delta := choice.Delta.Content; delta != "" {
			if err := s.ensureTextItem(w, flusher); err != nil {
				return err
			}
			s.textBuilder.WriteString(delta)
			if err := writeSSEEvent(w, flusher, map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       s.textItemID,
				"output_index":  s.textOutputIdx,
				"content_index": 0,
				"delta":         delta,
			}, s.debugVerbose); err != nil {
				return err
			}
		}
	}

	return nil
}

// ensureTextItem 按需发送 response.output_item.added 和 response.content_part.added 事件。
// 仅在首次收到文本内容时触发一次，建立 Codex 侧的消息条目和内容片段。
func (s *responseStreamState) ensureTextItem(w io.Writer, flusher http.Flusher) error {
	if s.textOutputIdx < 0 {
		s.textOutputIdx = s.nextOutputIdx
		s.nextOutputIdx++
	}

	if !s.itemSent {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.output_item.added",
			"output_index": s.textOutputIdx,
			"item": map[string]any{
				"id":     s.textItemID,
				"type":   "message",
				"status": "in_progress",
				"role":   "assistant",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": "",
					},
				},
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		s.itemSent = true
	}

	if !s.partSent {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":          "response.content_part.added",
			"item_id":       s.textItemID,
			"output_index":  s.textOutputIdx,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": "",
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		s.partSent = true
	}

	return nil
}

// consumeToolCall 处理单个工具调用的流式增量。
// 首次出现发送 response.output_item.added，后续 arguments 增量发送 response.function_call_arguments.delta。
func (s *responseStreamState) consumeToolCall(toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}, w io.Writer, flusher http.Flusher) error {
	// 查找或创建该 index 对应的工具调用状态
	state, exists := s.toolCalls[toolCall.Index]
	if !exists {
		callID := util.FirstNonEmpty(strings.TrimSpace(toolCall.ID), fmt.Sprintf("call-%s-%d", s.responseID, toolCall.Index))
		state = &toolCallState{
			index:     toolCall.Index,
			outputIdx: s.nextOutputIdx,
			itemID:    "fc-" + callID,
			callID:    callID,
		}
		s.nextOutputIdx++
		s.toolCalls[toolCall.Index] = state
	}

	// 累积 name 字段（可能在多个 chunk 中分段到达）

	if name := strings.TrimSpace(toolCall.Function.Name); name != "" {
		state.name = name
	}
	// 累积 arguments 字段（流式分段拼接）
	if args := toolCall.Function.Arguments; args != "" {
		state.args.WriteString(args)
	}

	// 首次到达时发送 output_item.added 事件

	if !state.itemSent {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.output_item.added",
			"output_index": state.outputIdx,
			"item": map[string]any{
				"id":        state.itemID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   state.callID,
				"name":      state.name,
				"arguments": "",
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		state.itemSent = true
	}

	// 发送 function_call_arguments.delta 事件

	if delta := toolCall.Function.Arguments; delta != "" {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.function_call_arguments.delta",
			"item_id":      state.itemID,
			"output_index": state.outputIdx,
			"delta":        delta,
		}, s.debugVerbose); err != nil {
			return err
		}
	}

	return nil
}

func (s *responseStreamState) finish(w io.Writer, flusher http.Flusher) error {
	// 如果从未发送过 created 事件（无有效 chunk），则无需收尾
	if !s.createdSent {
		return nil
	}

	type outputItem struct {
		outputIdx int
		item      map[string]any
	}
	outputItems := make([]outputItem, 0, 1+len(s.toolCalls))
	text := s.textBuilder.String()

	// ★ 收尾文本输出：content_part.done → output_text.done → output_item.done
	//   三个事件的 send 顺序对应 Codex 的生命周期：片段完成 → 文本完成 → 条目完成

	if s.partSent {
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":          "response.content_part.done",
			"item_id":       s.textItemID,
			"output_index":  s.textOutputIdx,
			"content_index": 0,
			"part": map[string]any{
				"type": "output_text",
				"text": text,
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":          "response.output_text.done",
			"item_id":       s.textItemID,
			"output_index":  s.textOutputIdx,
			"content_index": 0,
			"text":          text,
		}, s.debugVerbose); err != nil {
			return err
		}
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.output_item.done",
			"output_index": s.textOutputIdx,
			"item": map[string]any{
				"id":     s.textItemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": text,
					},
				},
			},
		}, s.debugVerbose); err != nil {
			return err
		}
		outputItems = append(outputItems, outputItem{
			outputIdx: s.textOutputIdx,
			item: map[string]any{
				"id":     s.textItemID,
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{
						"type": "output_text",
						"text": text,
					},
				},
			}})
	}

	// ★ 收尾工具调用：按 index 排序后发送 function_call_arguments.done → output_item.done

	toolIndexes := make([]int, 0, len(s.toolCalls))
	for index := range s.toolCalls {
		toolIndexes = append(toolIndexes, index)
	}
	slices.Sort(toolIndexes)

	for _, index := range toolIndexes {
		state := s.toolCalls[index]
		arguments := state.args.String()
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.function_call_arguments.done",
			"item_id":      state.itemID,
			"output_index": state.outputIdx,
			"arguments":    arguments,
		}, s.debugVerbose); err != nil {
			return err
		}

		item := map[string]any{
			"id":        state.itemID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   state.callID,
			"name":      state.name,
			"arguments": arguments,
		}
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":         "response.output_item.done",
			"output_index": state.outputIdx,
			"item":         item,
		}, s.debugVerbose); err != nil {
			return err
		}
		outputItems = append(outputItems, outputItem{
			outputIdx: state.outputIdx,
			item:      item,
		})
	}

	slices.SortFunc(outputItems, func(a, b outputItem) int {
		return a.outputIdx - b.outputIdx
	})
	output := make([]map[string]any, 0, len(outputItems))
	for _, item := range outputItems {
		output = append(output, item.item)
	}

	// ★ 最后发送 response.completed，汇总所有 output 条目

	return writeSSEEvent(w, flusher, map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":         s.responseID,
			"object":     "response",
			"created_at": s.createdAt,
			"model":      s.model,
			"status":     "completed",
			"output":     output,
		},
	}, s.debugVerbose)
}

// writeSSEEvent 将 Codex 事件序列化为 JSON 后以 SSE 格式写入。
// 格式：data: {JSON}\n\n
func writeSSEEvent(w io.Writer, flusher http.Flusher, payload any, verbose bool) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if verbose {
		log.Printf("[proxy-debug-sse-out] data=%s", data)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
