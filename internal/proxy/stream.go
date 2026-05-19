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
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := &responseStreamState{debugVerbose: verbose}
	chunkCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if data == "" {
			continue
		}
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

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			if debug {
				log.Printf("[proxy-debug] stream-skip-invalid-json len=%d", len(data))
			}
			continue
		}
		chunkCount++

		if err := state.consumeChunk(chunk, w, flusher); err != nil {
			if debug {
				log.Printf("[proxy-debug] stream-consume-error chunks=%d err=%v", chunkCount, err)
			}
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if debug {
			log.Printf("[proxy-debug] stream-scan-error chunks=%d err=%v", chunkCount, err)
		}
		return err
	}

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
	if s.responseID == "" {
		s.responseID = util.FirstNonEmpty(strings.TrimSpace(chunk.ID), "resp-proxy")
		s.textItemID = "msg-" + s.responseID
		s.textOutputIdx = -1
		s.model = strings.TrimSpace(chunk.Model)
		s.createdAt = chunk.Created
		s.toolCalls = map[int]*toolCallState{}
	}

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

	for _, choice := range chunk.Choices {
		for _, toolCall := range choice.Delta.ToolCalls {
			if err := s.consumeToolCall(toolCall, w, flusher); err != nil {
				return err
			}
		}

		if delta := choice.Delta.Content; delta != "" {
			if err := s.ensureTextItem(w, flusher); err != nil {
				return err
			}
			s.textBuilder.WriteString(delta)
			if err := writeSSEEvent(w, flusher, map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       s.textItemID,
				"output_index":  0,
				"content_index": 0,
				"delta":         delta,
			}, s.debugVerbose); err != nil {
				return err
			}
		}
	}

	return nil
}

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

func (s *responseStreamState) consumeToolCall(toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}, w io.Writer, flusher http.Flusher) error {
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

	if name := strings.TrimSpace(toolCall.Function.Name); name != "" {
		state.name = name
	}
	if args := toolCall.Function.Arguments; args != "" {
		state.args.WriteString(args)
	}

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
	if !s.createdSent {
		return nil
	}

	output := make([]map[string]any, 0, 1+len(s.toolCalls))
	text := s.textBuilder.String()

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
		output = append(output, map[string]any{
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
		})
	}

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
		output = append(output, item)
	}

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