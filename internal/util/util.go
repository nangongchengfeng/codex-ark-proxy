package util

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FirstNonEmpty 返回第一个非空字符串值；若所有值均为空，则返回空字符串。
// 常用于环境变量优先级查找：先检查新变量名，再回退到旧变量名，最后使用默认值。
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// TrimStringValue 将 any 类型安全转换为 string 并去除首尾空白。
// 若值不是 string 类型，返回空字符串。
func TrimStringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// StringifyValue 将任意值转为 JSON 字符串表示。
// 对于 nil 返回空字符串；对于 string 直接返回去空白后的值；
// 对于其他类型通过 json.Marshal 序列化。
func StringifyValue(value any) string {
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

// NormalizeContentValue 规范化消息内容字段，支持以下格式：
//   - 纯文本字符串
//   - OpenAI 风格的 content 数组（text / input_text / output_text）
//
// 返回规范化的文本内容，若内容为空则返回 nil。
func NormalizeContentValue(value any) any {
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
				text := FirstNonEmpty(
					TrimStringValue(part["text"]),
					TrimStringValue(part["input_text"]),
					TrimStringValue(part["output_text"]),
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

// TruncateLogBody 截断日志内容到指定长度，超出部分用 "...(truncated)" 标记。
func TruncateLogBody(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return fmt.Sprintf("%s...(truncated)", value[:limit])
}
