package util

import "net/http"

// CopyRequestHeaders 将客户端请求头复制到上游请求中，但过滤掉：
//   - Authorization（由代理自行设置上游 API Key）
//   - 逐跳头（Hop-by-hop headers）
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

// CopyResponseHeaders 将上游响应头复制到客户端响应中，过滤逐跳头。
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

// IsHopByHopHeader 判断是否为 HTTP/1.1 定义的逐跳头，这些头不应由代理转发。
func IsHopByHopHeader(header string) bool {
	switch header {
	case "Connection", "Keep-Alive", "Proxy-Authenticate",
		"Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}