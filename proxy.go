package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"
)

type Proxy struct {
	cfg    Config
	client *http.Client
}

func NewProxy(cfg Config, client *http.Client) *Proxy {
	if client == nil {
		client = &http.Client{Timeout: cfg.UpstreamTimeout}
	}

	return &Proxy{
		cfg:    cfg,
		client: client,
	}
}

func (p *Proxy) HandleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if p == nil || p.client == nil {
		http.Error(w, "proxy not configured", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body failed", http.StatusBadRequest)
		return
	}
	p.debugLogRequest("incoming", body)

	payload, err := transformRequestPayload(body, p.cfg.Model, p.cfg.ForceModelOverride)
	if err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	p.debugLogRequest("transformed", payload)

	targetURL := p.cfg.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		http.Error(w, "create upstream request failed", http.StatusInternalServerError)
		return
	}

	copyRequestHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", defaultContentType)
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		log.Printf("upstream request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if p.cfg.DebugProxy {
		log.Printf("[proxy-debug] upstream-status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	streaming := isEventStream(resp.Header)
	var flusher http.Flusher
	if streaming {
		var ok bool
		flusher, ok = w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	if streaming {
		if err := streamResponse(w, resp.Body, flusher); err != nil {
			log.Printf("stream upstream response failed: %v", err)
		}
	} else {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			log.Printf("copy upstream response failed: %v", readErr)
			return
		}
		p.debugLogResponse(resp.StatusCode, responseBody)
		if _, err := w.Write(responseBody); err != nil {
			log.Printf("copy upstream response failed: %v", err)
		}
	}

	log.Printf("upstream POST %s -> %d (%s)", targetURL, resp.StatusCode, time.Since(start))
}

func (p *Proxy) debugLogRequest(stage string, body []byte) {
	if !p.cfg.DebugProxy {
		return
	}

	log.Printf("[proxy-debug] stage=%s summary=%s", stage, summarizePayload(body))
}

func (p *Proxy) debugLogResponse(status int, body []byte) {
	if !p.cfg.DebugProxy {
		return
	}

	log.Printf("[proxy-debug] upstream-response status=%d body=%s", status, truncateLogBody(string(body), 1200))
}

func transformRequestPayload(body []byte, fallback string, forceModelOverride bool) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		if fallback == "" {
			return body, nil
		}
		return json.Marshal(map[string]any{"model": fallback})
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	model, ok := payload["model"].(string)
	if forceModelOverride && strings.TrimSpace(fallback) != "" {
		payload["model"] = fallback
	} else if !ok || strings.TrimSpace(model) == "" {
		if fallback != "" {
			payload["model"] = fallback
		}
	}

	if _, exists := payload["messages"]; !exists {
		messages := make([]map[string]any, 0, 2)

		if instructions := trimStringValue(payload["instructions"]); instructions != "" {
			messages = append(messages, map[string]any{
				"role":    "system",
				"content": instructions,
			})
		}

		messages = append(messages, buildMessagesFromInput(payload["input"])...)
		if len(messages) > 0 {
			payload["messages"] = messages
		}
	}

	if messages, ok := payload["messages"].([]any); ok {
		payload["messages"] = normalizeMessages(messages)
	}

	if _, exists := payload["max_output_tokens"]; exists {
		if _, hasMaxTokens := payload["max_tokens"]; !hasMaxTokens {
			payload["max_tokens"] = payload["max_output_tokens"]
		}
		delete(payload, "max_output_tokens")
	}

	if tools, ok := payload["tools"].([]any); ok {
		normalizedTools := normalizeTools(tools)
		if len(normalizedTools) > 0 {
			payload["tools"] = normalizedTools
		} else {
			delete(payload, "tools")
			delete(payload, "tool_choice")
		}
	}

	delete(payload, "input")
	delete(payload, "instructions")

	return json.Marshal(payload)
}

func buildMessagesFromInput(input any) []map[string]any {
	switch value := input.(type) {
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil
		}
		return []map[string]any{{
			"role":    "user",
			"content": text,
		}}
	case []any:
		messages := make([]map[string]any, 0, len(value))
		for _, item := range value {
			messages = append(messages, buildMessagesFromInputItem(item)...)
		}
		return messages
	case map[string]any:
		return buildMessagesFromInputItem(value)
	default:
		return nil
	}
}

func buildMessagesFromInputItem(value any) []map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		message := buildSingleMessage(value)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	}

	switch trimStringValue(item["type"]) {
	case "", "message":
		message := buildSingleMessage(item)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	case "function_call":
		callID := firstNonEmpty(trimStringValue(item["call_id"]), trimStringValue(item["tool_call_id"]))
		name := trimStringValue(item["name"])
		arguments := stringifyValue(item["arguments"])
		if callID == "" || name == "" {
			return nil
		}
		return []map[string]any{{
			"role": "assistant",
			"tool_calls": []map[string]any{
				{
					"id":   callID,
					"type": "function",
					"function": map[string]any{
						"name":      name,
						"arguments": arguments,
					},
				},
			},
		}}
	case "function_call_output":
		callID := firstNonEmpty(trimStringValue(item["call_id"]), trimStringValue(item["tool_call_id"]))
		output := stringifyValue(item["output"])
		if callID == "" {
			return nil
		}
		return []map[string]any{{
			"role":         "tool",
			"tool_call_id": callID,
			"content":      output,
		}}
	default:
		message := buildSingleMessage(item)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	}
}

func buildSingleMessage(value any) map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	role := trimStringValue(item["role"])
	if role == "" {
		role = "user"
	}

	content := normalizeContentValue(item["content"])
	if content == nil {
		if text := firstNonEmpty(trimStringValue(item["text"]), trimStringValue(item["input_text"])); text != "" {
			content = text
		}
	}
	if content == nil {
		return nil
	}

	return map[string]any{
		"role":    normalizeRole(role),
		"content": content,
	}
}

func normalizeMessages(messages []any) []map[string]any {
	normalized := make([]map[string]any, 0, len(messages))
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}

		role := normalizeRole(trimStringValue(message["role"]))
		if role == "" {
			role = "user"
		}

		normalizedMessage := map[string]any{
			"role": role,
		}
		if content, exists := message["content"]; exists {
			normalizedMessage["content"] = content
		}
		if name, exists := message["name"]; exists {
			normalizedMessage["name"] = name
		}
		if toolCallID, exists := message["tool_call_id"]; exists {
			normalizedMessage["tool_call_id"] = toolCallID
		}
		if toolCalls, exists := message["tool_calls"]; exists {
			normalizedMessage["tool_calls"] = toolCalls
		}

		normalized = append(normalized, normalizedMessage)
	}
	return normalized
}

func normalizeRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "", "user":
		return "user"
	case "developer", "system":
		return "system"
	case "assistant":
		return "assistant"
	case "tool":
		return "tool"
	default:
		return role
	}
}

func normalizeTools(tools []any) []map[string]any {
	normalized := make([]map[string]any, 0, len(tools))
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}

		toolType := trimStringValue(tool["type"])
		if toolType == "" {
			toolType = "function"
		}
		if toolType != "function" {
			continue
		}

		normalizedTool := map[string]any{
			"type": toolType,
		}

		if functionBlock, ok := tool["function"].(map[string]any); ok {
			normalizedTool["function"] = functionBlock
			normalized = append(normalized, normalizedTool)
			continue
		}

		functionBlock := map[string]any{}
		if name := trimStringValue(tool["name"]); name != "" {
			functionBlock["name"] = name
		}
		if description := trimStringValue(tool["description"]); description != "" {
			functionBlock["description"] = description
		}
		if parameters, exists := tool["parameters"]; exists {
			functionBlock["parameters"] = parameters
		}
		if strict, exists := tool["strict"]; exists {
			functionBlock["strict"] = strict
		}

		if len(functionBlock) > 0 {
			normalizedTool["function"] = functionBlock
		}

		normalized = append(normalized, normalizedTool)
	}

	return normalized
}

func summarizePayload(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return "empty-body"
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Sprintf("invalid-json len=%d", len(body))
	}

	parts := []string{
		fmt.Sprintf("keys=%v", sortedKeys(payload)),
		fmt.Sprintf("model=%q", trimStringValue(payload["model"])),
		fmt.Sprintf("has_messages=%t", payload["messages"] != nil),
		fmt.Sprintf("has_input=%t", payload["input"] != nil),
		fmt.Sprintf("has_tools=%t", payload["tools"] != nil),
		fmt.Sprintf("tool_choice=%q", trimStringValue(payload["tool_choice"])),
	}

	if messages, ok := payload["messages"].([]any); ok {
		parts = append(parts, "message_roles="+strings.Join(extractMessageRoles(messages), ","))
		parts = append(parts, "message_details="+strings.Join(extractMessageDetails(messages), ";"))
	}

	if tools, ok := payload["tools"].([]any); ok {
		parts = append(parts, "tools="+strings.Join(extractToolSummaries(tools), ";"))
	}

	if input, exists := payload["input"]; exists {
		parts = append(parts, "input_details="+strings.Join(extractInputSummaries(input), ";"))
	}

	return strings.Join(parts, " ")
}

func sortedKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func extractMessageRoles(messages []any) []string {
	roles := make([]string, 0, len(messages))
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			roles = append(roles, "invalid")
			continue
		}
		roles = append(roles, trimStringValue(message["role"]))
	}
	return roles
}

func extractMessageDetails(messages []any) []string {
	details := make([]string, 0, len(messages))
	for idx, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			details = append(details, fmt.Sprintf("%d:invalid", idx))
			continue
		}

		details = append(details, fmt.Sprintf("%d:role=%s,keys=%v,tool_call_id=%s,tool_calls=%t", idx, trimStringValue(message["role"]), sortedKeys(message), trimStringValue(message["tool_call_id"]), message["tool_calls"] != nil))
	}
	return details
}

func extractInputSummaries(input any) []string {
	switch value := input.(type) {
	case []any:
		summaries := make([]string, 0, len(value))
		for idx, item := range value {
			summaries = append(summaries, summarizeInputItem(idx, item))
		}
		return summaries
	case map[string]any:
		return []string{summarizeInputItem(0, value)}
	case string:
		if strings.TrimSpace(value) == "" {
			return []string{"0:string-empty"}
		}
		return []string{"0:string"}
	default:
		return nil
	}
}

func summarizeInputItem(idx int, item any) string {
	entry, ok := item.(map[string]any)
	if !ok {
		return fmt.Sprintf("%d:type=%T", idx, item)
	}

	itemType := trimStringValue(entry["type"])
	role := trimStringValue(entry["role"])
	callID := firstNonEmpty(trimStringValue(entry["call_id"]), trimStringValue(entry["tool_call_id"]))
	name := trimStringValue(entry["name"])
	if name == "" {
		if function, ok := entry["function"].(map[string]any); ok {
			name = trimStringValue(function["name"])
		}
	}

	return fmt.Sprintf("%d:type=%s,role=%s,call_id=%s,name=%s,keys=%v", idx, itemType, role, callID, name, sortedKeys(entry))
}

func extractToolSummaries(tools []any) []string {
	summaries := make([]string, 0, len(tools))
	for idx, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			summaries = append(summaries, fmt.Sprintf("%d:invalid", idx))
			continue
		}

		toolType := trimStringValue(tool["type"])
		name := trimStringValue(tool["name"])
		functionName := ""
		if functionBlock, ok := tool["function"].(map[string]any); ok {
			functionName = trimStringValue(functionBlock["name"])
		}

		summaries = append(summaries, fmt.Sprintf("%d:type=%s,name=%s,function=%s,keys=%v", idx, toolType, name, functionName, sortedKeys(tool)))
	}
	return summaries
}

func truncateLogBody(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}

func normalizeContentValue(value any) any {
	switch content := value.(type) {
	case string:
		text := strings.TrimSpace(content)
		if text == "" {
			return nil
		}
		return text
	case []any:
		parts := make([]string, 0, len(content))
		for _, item := range content {
			switch part := item.(type) {
			case string:
				text := strings.TrimSpace(part)
				if text != "" {
					parts = append(parts, text)
				}
			case map[string]any:
				text := firstNonEmpty(
					trimStringValue(part["text"]),
					trimStringValue(part["input_text"]),
					trimStringValue(part["output_text"]),
				)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		joined := strings.TrimSpace(strings.Join(parts, "\n"))
		if joined == "" {
			return nil
		}
		return joined
	default:
		return nil
	}
}

func trimStringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func copyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "Authorization" || isHopByHopHeader(canonical) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(http.CanonicalHeaderKey(key)) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func streamResponse(w io.Writer, body io.Reader, flusher http.Flusher) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	state := &responseStreamState{}
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
			break
		}

		var chunk chatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if err := state.consumeChunk(chunk, w, flusher); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if err := state.finish(w, flusher); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

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

	toolCalls map[int]*toolCallState
}

type toolCallState struct {
	index     int
	outputIdx int
	itemID    string
	callID    string
	name      string
	args      strings.Builder
	itemSent  bool
}

func (s *responseStreamState) consumeChunk(chunk chatCompletionChunk, w io.Writer, flusher http.Flusher) error {
	if s.responseID == "" {
		s.responseID = firstNonEmpty(strings.TrimSpace(chunk.ID), "resp-proxy")
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
		}); err != nil {
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
			}); err != nil {
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
		}); err != nil {
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
		}); err != nil {
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
		callID := firstNonEmpty(strings.TrimSpace(toolCall.ID), fmt.Sprintf("call-%s-%d", s.responseID, toolCall.Index))
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
		}); err != nil {
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
		}); err != nil {
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
		}); err != nil {
			return err
		}
		if err := writeSSEEvent(w, flusher, map[string]any{
			"type":          "response.output_text.done",
			"item_id":       s.textItemID,
			"output_index":  s.textOutputIdx,
			"content_index": 0,
			"text":          text,
		}); err != nil {
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
		}); err != nil {
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
		}); err != nil {
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
		}); err != nil {
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
	})
}

func writeSSEEvent(w io.Writer, flusher http.Flusher, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func isEventStream(headers http.Header) bool {
	return strings.Contains(strings.ToLower(headers.Get("Content-Type")), "text/event-stream")
}

func isHopByHopHeader(header string) bool {
	switch header {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
