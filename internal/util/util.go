package util

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FirstNonEmpty 返回第一个非空字符串值。
// 用于环境变量优先级链：先检查新变量名 → 回退旧变量名 → 最后使用默认值。
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// TrimStringValue 将 any 安全转为 string 并去空白，非 string 类型返回空字符串。
func TrimStringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// StringifyValue 将任意值转为 JSON 字符串。
// nil → ""；string → 去空白后返回；其他 → json.Marshal 序列化。
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

// NormalizeContentValue 规范化消息的 content 字段，统一处理以下格式：
//   - 纯文本字符串 → 去空白直接返回
//   - content 数组（OpenAI 风格） → 拼接 text/input_text/output_text
//   - 空内容 → 返回 nil
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

// TruncateLogBody 截断日志字符串到指定长度，超出部分标记 "...(truncated)"。
func TruncateLogBody(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return fmt.Sprintf("%s...(truncated)", value[:limit])
}
