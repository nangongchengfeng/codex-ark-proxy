package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxyForwardsResponsesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"model":"glm-5.1"`)) {
			t.Fatalf("expected model fallback, got %s", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	req.Header.Set("Authorization", "Bearer client-value")
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"id":"ok"}` {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestProxyTransformsResponsesInputToMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		messages, ok := payload["messages"].([]any)
		if !ok {
			t.Fatalf("expected messages array, got %T", payload["messages"])
		}
		if len(messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(messages))
		}
		if _, exists := payload["input"]; exists {
			t.Fatalf("expected input to be removed, got %s", string(body))
		}
		if _, exists := payload["instructions"]; exists {
			t.Fatalf("expected instructions to be removed, got %s", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","instructions":"你是代码助手","input":"帮我分析这个函数","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestProxyMapsDeveloperRoleToSystem(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		messages, ok := payload["messages"].([]any)
		if !ok || len(messages) != 2 {
			t.Fatalf("unexpected messages payload: %s", string(body))
		}

		first, ok := messages[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected first message type: %T", messages[0])
		}
		second, ok := messages[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected second message type: %T", messages[1])
		}

		if first["role"] != "system" {
			t.Fatalf("expected developer role to map to system, got %v", first["role"])
		}
		if second["role"] != "user" {
			t.Fatalf("expected user role to stay user, got %v", second["role"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","input":[{"role":"developer","content":"你是代码助手"},{"role":"user","content":"请解释这个报错"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestProxyNormalizesExistingMessagesRole(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		messages, ok := payload["messages"].([]any)
		if !ok || len(messages) != 1 {
			t.Fatalf("unexpected messages payload: %s", string(body))
		}

		message, ok := messages[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected message type: %T", messages[0])
		}

		if message["role"] != "system" {
			t.Fatalf("expected developer role to map to system, got %v", message["role"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","messages":[{"role":"developer","content":"你是代码助手"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestProxyTransformsResponsesToolsToChatCompletionsTools(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("unexpected tools payload: %s", string(body))
		}

		tool, ok := tools[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool type: %T", tools[0])
		}

		if tool["type"] != "function" {
			t.Fatalf("expected function tool type, got %v", tool["type"])
		}

		functionBlock, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("expected function block, got %T", tool["function"])
		}

		if functionBlock["name"] != "run_command" {
			t.Fatalf("unexpected function name: %v", functionBlock["name"])
		}

		if _, exists := tool["name"]; exists {
			t.Fatalf("expected top-level tool name to be removed, got %s", string(body))
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","input":"hi","tools":[{"type":"function","name":"run_command","description":"run shell command","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestProxyDropsUnsupportedNonFunctionTools(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("expected only one compatible tool, got %s", string(body))
		}

		tool, ok := tools[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool type: %T", tools[0])
		}

		if tool["type"] != "function" {
			t.Fatalf("expected function tool type, got %v", tool["type"])
		}

		functionBlock, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("expected function block, got %T", tool["function"])
		}

		if functionBlock["name"] != "shell_command" {
			t.Fatalf("unexpected function name: %v", functionBlock["name"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","input":"hi","tools":[{"type":"function","name":"shell_command","description":"run shell command","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},{"type":"web_search","external_web_access":true}],"tool_choice":"auto","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestProxyStreamsSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"created\":123,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"created\":123,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"content\":\" there\"},\"finish_reason\":\"stop\"}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected content type: %s", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.created"`) {
		t.Fatalf("expected response.created event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.output_text.delta"`) {
		t.Fatalf("expected response.output_text.delta event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.output_item.added"`) {
		t.Fatalf("expected response.output_item.added event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.content_part.added"`) {
		t.Fatalf("expected response.content_part.added event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.output_item.done"`) {
		t.Fatalf("expected response.output_item.done event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("expected response.completed event, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"text":"hi there"`) {
		t.Fatalf("expected completed text, got %s", rec.Body.String())
	}
}

func TestProxyStreamsFunctionCallEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"created\":456,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_123\",\"type\":\"function\",\"function\":{\"name\":\"shell_command\",\"arguments\":\"{\\\"command\\\":\"}}]}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-tool\",\"created\":456,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"echo hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"response.output_item.added"`) || !strings.Contains(body, `"type":"function_call"`) {
		t.Fatalf("expected function_call item added event, got %s", body)
	}
	if !strings.Contains(body, `"type":"response.function_call_arguments.delta"`) {
		t.Fatalf("expected function_call_arguments.delta event, got %s", body)
	}
	if !strings.Contains(body, `"type":"response.function_call_arguments.done"`) {
		t.Fatalf("expected function_call_arguments.done event, got %s", body)
	}
	if !strings.Contains(body, `"name":"shell_command"`) {
		t.Fatalf("expected function name in stream, got %s", body)
	}
	if !strings.Contains(body, `{\"command\":\"echo hi\"}`) && !strings.Contains(body, `"arguments":"{\"command\":\"echo hi\"}"`) {
		t.Fatalf("expected function arguments in stream, got %s", body)
	}
}

func TestProxyStreamsMixedTextAndFunctionCallWithDistinctOutputIndexes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-mixed\",\"created\":789,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Running tool: \"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-mixed\",\"created\":789,\"model\":\"glm-5.1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_mix\",\"type\":\"function\",\"function\":{\"name\":\"shell_command\",\"arguments\":\"{\\\"command\\\":\\\"echo hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"response.output_item.added"`) {
		t.Fatalf("expected output item events, got %s", body)
	}
	if !strings.Contains(body, `"output_index":0`) || !strings.Contains(body, `"output_index":1`) {
		t.Fatalf("expected distinct output indexes for message and function call, got %s", body)
	}
}

func TestProxyTransformsFunctionCallOutputIntoToolMessage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unexpected json payload: %v", err)
		}

		messages, ok := payload["messages"].([]any)
		if !ok || len(messages) != 3 {
			t.Fatalf("expected 3 messages, got %s", string(body))
		}

		userMsg := messages[0].(map[string]any)
		assistantMsg := messages[1].(map[string]any)
		toolMsg := messages[2].(map[string]any)

		if userMsg["role"] != "user" {
			t.Fatalf("expected first message to be user, got %v", userMsg["role"])
		}

		if assistantMsg["role"] != "assistant" {
			t.Fatalf("expected second message to be assistant, got %v", assistantMsg["role"])
		}
		toolCalls, ok := assistantMsg["tool_calls"].([]any)
		if !ok || len(toolCalls) != 1 {
			t.Fatalf("expected assistant tool_calls, got %v", assistantMsg["tool_calls"])
		}

		if toolMsg["role"] != "tool" {
			t.Fatalf("expected third message to be tool, got %v", toolMsg["role"])
		}
		if toolMsg["tool_call_id"] != "call_123" {
			t.Fatalf("expected tool_call_id call_123, got %v", toolMsg["tool_call_id"])
		}
		if toolMsg["content"] != "hello" {
			t.Fatalf("expected tool output content hello, got %v", toolMsg["content"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	reqBody := `{"model":"glm-5.1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"创建一个 hellos.txt 文件，写入 hello"}]},{"type":"function_call","call_id":"call_123","name":"shell_command","arguments":"{\"command\":\"Set-Content -Path \\\"hellos.txt\\\" -Value \\\"hello\\\"\"}"},{"type":"function_call_output","call_id":"call_123","output":"hello"}],"stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTransformRequestPayloadForceModelOverride(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4-mini","input":"hi"}`)

	payload, err := transformRequestPayload(body, "doubao-seed-2-0-code-preview-260215", true)
	if err != nil {
		t.Fatalf("transformRequestPayload returned error: %v", err)
	}

	var transformed map[string]any
	if err := json.Unmarshal(payload, &transformed); err != nil {
		t.Fatalf("unexpected transformed payload: %v", err)
	}

	if transformed["model"] != "doubao-seed-2-0-code-preview-260215" {
		t.Fatalf("expected model override, got %v", transformed["model"])
	}
}

func TestSummarizePayloadIncludesModel(t *testing.T) {
	summary := summarizePayload([]byte(`{"model":"doubao-seed-2-0-code-preview-260215","input":"hi"}`))
	if !strings.Contains(summary, `model="doubao-seed-2-0-code-preview-260215"`) {
		t.Fatalf("expected summary to include model, got %s", summary)
	}
}

func TestProxyPassesThroughUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer upstream.Close()

	proxy := NewProxy(Config{BaseURL: upstream.URL, APIKey: "secret", Model: "glm-5.1"}, upstream.Client())
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":false}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"error":"unauthorized"}` {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestProxyReturnsBadGatewayOnUpstreamFailure(t *testing.T) {
	client := &http.Client{
		Timeout: 20 * time.Millisecond,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed")
		}),
	}

	proxy := NewProxy(Config{BaseURL: "https://example.com", APIKey: "secret", Model: "glm-5.1"}, client)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"stream":false}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	proxy.HandleResponses(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
