package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"proxy_doubao/internal/util"
)

// transformRequestPayload 将 Codex /v1/responses 格式的请求体转换为 OpenAI /chat/completions 格式。
func transformRequestPayload(body []byte, fallback string, forceModelOverride bool) ([]byte, error) {
	if len(body) == 0 {
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

		if instructions := util.TrimStringValue(payload["instructions"]); instructions != "" {
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

// buildMessagesFromInput 将 Codex 的 input 字段转换为 OpenAI 标准 messages 数组。
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

// buildMessagesFromInputItem 根据 Codex input 条目的 type 字段进行分派转换。
func buildMessagesFromInputItem(value any) []map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		message := buildSingleMessage(value)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	}

	switch util.TrimStringValue(item["type"]) {
	case "", "message":
		message := buildSingleMessage(item)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	case "function_call":
		return buildFunctionCallMessage(item)
	case "function_call_output":
		return buildFunctionCallOutputMessage(item)
	default:
		message := buildSingleMessage(item)
		if message == nil {
			return nil
		}
		return []map[string]any{message}
	}
}

func buildFunctionCallMessage(item map[string]any) []map[string]any {
	callID := util.FirstNonEmpty(util.TrimStringValue(item["call_id"]), util.TrimStringValue(item["tool_call_id"]))
	name := util.TrimStringValue(item["name"])
	arguments := util.StringifyValue(item["arguments"])
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
}

func buildFunctionCallOutputMessage(item map[string]any) []map[string]any {
	callID := util.FirstNonEmpty(util.TrimStringValue(item["call_id"]), util.TrimStringValue(item["tool_call_id"]))
	output := util.StringifyValue(item["output"])
	if callID == "" {
		return nil
	}
	return []map[string]any{{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      output,
	}}
}

// buildSingleMessage 从 Codex input 条目中提取 role 和 content，构建单条标准消息。
func buildSingleMessage(value any) map[string]any {
	item, ok := value.(map[string]any)
	if !ok {
		return nil
	}

	role := util.TrimStringValue(item["role"])
	if role == "" {
		role = "user"
	}

	content := util.NormalizeContentValue(item["content"])
	if content == nil {
		if text := util.FirstNonEmpty(util.TrimStringValue(item["text"]), util.TrimStringValue(item["input_text"])); text != "" {
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

// normalizeMessages 规范化 messages 数组中的每条消息。
func normalizeMessages(messages []any) []map[string]any {
	normalized := make([]map[string]any, 0, len(messages))
	for _, item := range messages {
		message, ok := item.(map[string]any)
		if !ok {
			continue
		}

		role := normalizeRole(util.TrimStringValue(message["role"]))
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

// normalizeRole 将角色名映射到上游支持的四种标准角色。developer → system
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

// normalizeTools 规范化 tools 数组，只保留 type="function" 的工具。
func normalizeTools(tools []any) []map[string]any {
	normalized := make([]map[string]any, 0, len(tools))
	for _, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			continue
		}

		toolType := util.TrimStringValue(tool["type"])
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
		if name := util.TrimStringValue(tool["name"]); name != "" {
			functionBlock["name"] = name
		}
		if description := util.TrimStringValue(tool["description"]); description != "" {
			functionBlock["description"] = description
		}
		if parameters, exists := tool["parameters"]; exists {
			functionBlock["parameters"] = parameters
		}
		if strct, exists := tool["strict"]; exists {
			functionBlock["strict"] = strct
		}

		if len(functionBlock) > 0 {
			normalizedTool["function"] = functionBlock
		}

		normalized = append(normalized, normalizedTool)
	}

	return normalized
}