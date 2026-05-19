package proxy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"proxy_doubao/internal/util"
)

// summarizePayload 将请求/响应 JSON 载荷压缩为一行摘要，方便调试日志。
func summarizePayload(body []byte) string {
	if len(body) == 0 {
		return "empty-body"
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Sprintf("invalid-json len=%d", len(body))
	}

	parts := []string{
		fmt.Sprintf("keys=%v", sortedKeys(payload)),
		fmt.Sprintf("model=%q", util.TrimStringValue(payload["model"])),
		fmt.Sprintf("has_messages=%t", payload["messages"] != nil),
		fmt.Sprintf("has_input=%t", payload["input"] != nil),
		fmt.Sprintf("has_tools=%t", payload["tools"] != nil),
		fmt.Sprintf("tool_choice=%q", util.TrimStringValue(payload["tool_choice"])),
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
		roles = append(roles, util.TrimStringValue(message["role"]))
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

		details = append(details, fmt.Sprintf("%d:role=%s,keys=%v,tool_call_id=%s,tool_calls=%t",
			idx,
			util.TrimStringValue(message["role"]),
			sortedKeys(message),
			util.TrimStringValue(message["tool_call_id"]),
			message["tool_calls"] != nil,
		))
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

	itemType := util.TrimStringValue(entry["type"])
	role := util.TrimStringValue(entry["role"])
	callID := util.FirstNonEmpty(util.TrimStringValue(entry["call_id"]), util.TrimStringValue(entry["tool_call_id"]))
	name := util.TrimStringValue(entry["name"])
	if name == "" {
		if function, ok := entry["function"].(map[string]any); ok {
			name = util.TrimStringValue(function["name"])
		}
	}

	return fmt.Sprintf("%d:type=%s,role=%s,call_id=%s,name=%s,keys=%v",
		idx, itemType, role, callID, name, sortedKeys(entry),
	)
}

func extractToolSummaries(tools []any) []string {
	summaries := make([]string, 0, len(tools))
	for idx, item := range tools {
		tool, ok := item.(map[string]any)
		if !ok {
			summaries = append(summaries, fmt.Sprintf("%d:invalid", idx))
			continue
		}

		toolType := util.TrimStringValue(tool["type"])
		name := util.TrimStringValue(tool["name"])
		functionName := ""
		if functionBlock, ok := tool["function"].(map[string]any); ok {
			functionName = util.TrimStringValue(functionBlock["name"])
		}

		summaries = append(summaries, fmt.Sprintf("%d:type=%s,name=%s,function=%s,keys=%v",
			idx, toolType, name, functionName, sortedKeys(tool),
		))
	}
	return summaries
}