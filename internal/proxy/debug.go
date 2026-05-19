package proxy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"proxy_doubao/internal/util"
)

// summarizePayload 将请求/响应 JSON 载荷压缩为一行可读摘要，用于调试日志。
// 摘要包含：顶层 key 列表、model、messages/input/tools 是否存在、消息角色序列、工具名称等。
func summarizePayload(body []byte) string {
	if len(body) == 0 {
		return "empty-body"
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Sprintf("invalid-json len=%d", len(body))
	}

	// 始终包含：keys、model、关键字段是否存在

	parts := []string{
		fmt.Sprintf("keys=%v", sortedKeys(payload)),
		fmt.Sprintf("model=%q", util.TrimStringValue(payload["model"])),
		fmt.Sprintf("has_messages=%t", payload["messages"] != nil),
		fmt.Sprintf("has_input=%t", payload["input"] != nil),
		fmt.Sprintf("has_tools=%t", payload["tools"] != nil),
		fmt.Sprintf("tool_choice=%q", util.TrimStringValue(payload["tool_choice"])),
	}

	// 展开 messages 数组的角色序列和详细信息

	if messages, ok := payload["messages"].([]any); ok {
		parts = append(parts, "message_roles="+strings.Join(extractMessageRoles(messages), ","))
		parts = append(parts, "message_details="+strings.Join(extractMessageDetails(messages), ";"))
	}

	// 展开 tools 数组的摘要信息

	if tools, ok := payload["tools"].([]any); ok {
		parts = append(parts, "tools="+strings.Join(extractToolSummaries(tools), ";"))
	}

	// 展开 input 字段的摘要信息

	if input, exists := payload["input"]; exists {
		parts = append(parts, "input_details="+strings.Join(extractInputSummaries(input), ";"))
	}

	return strings.Join(parts, " ")
}

// sortedKeys 返回 map 的 key 列表（按字母排序），确保日志可对比。
func sortedKeys(payload map[string]any) []string {
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// extractMessageRoles 提取消息数组中的 role 序列，如 "system,user,assistant"。
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

// extractMessageDetails 提取每条消息的详细信息：role、keys、tool_call_id、是否有 tool_calls。
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

// extractInputSummaries 根据 input 的类型（string/array/object）提取摘要信息。
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

// extractToolSummaries 提取 tools 数组中每个工具的类型、名称等信息。
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
