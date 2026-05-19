// header.go — HTTP 头复制工具，自动过滤敏感头和逐跳头
package util

import "net/http"

// CopyRequestHeaders 将客户端请求头复制到上游请求。
// 过滤规则：跳过 Authorization（代理自行注入 API Key）和所有逐跳头。
func CopyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "Authorization" || IsHopByHopHeader(canonical) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// CopyResponseHeaders 将上游响应头复制到客户端响应，仅过滤逐跳头。
func CopyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if IsHopByHopHeader(http.CanonicalHeaderKey(key)) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

// IsHopByHopHeader 判断是否为 RFC 7230 定义的逐跳头。
// 逐跳头仅对单次 HTTP 连接有效，不应由代理转发（Connection, Keep-Alive, Upgrade 等）。
func IsHopByHopHeader(header string) bool {
	switch header {
	case "Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
