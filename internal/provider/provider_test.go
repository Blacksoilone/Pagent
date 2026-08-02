package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ═══════════════ 基础消息类型 ═══════════════

func TestChatMessage_Should_MarshalToOpenAIFormat(t *testing.T) {
	msg := Message{Role: "user", Content: "你好"}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"role":"user"`) {
		t.Errorf("marshal = %s", b)
	}
}

// ═══════════════ 流式响应解析 ═══════════════

// mockStreamServer 返回 SSE 流：先是文本块，再是工具调用块。
func mockStreamServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handler))
}

func TestStream_Should_ParseTextAndToolCallChunks(t *testing.T) {
	srv := mockStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 验证请求格式
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if req.Model == "" {
			t.Error("model empty")
		}
		if req.Stream != true {
			t.Error("stream must be true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","content":"你好"},"index":0}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"世界"},"index":0}]}

data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/a.go\"}"}}]},"index":0}]}

data: [DONE]

`)
	})

	cl := &Client{BaseURL: srv.URL, APIKey: "test-key", Model: "deepseek-chat"}
	ctx := context.Background()

	msgs := []Message{{Role: "user", Content: "hi"}}
	var text strings.Builder
	var toolCalls []ToolCall

	err := cl.Stream(ctx, msgs, nil, func(p StreamPart) error {
		switch p.Type {
		case PartText:
			text.WriteString(p.Text)
		case PartToolCall:
			toolCalls = append(toolCalls, p.ToolCall)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.String() != "你好世界" {
		t.Errorf("text = %q, want 你好世界", text.String())
	}
	if len(toolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(toolCalls))
	}
	if toolCalls[0].Name != "read_file" {
		t.Errorf("tool name = %s", toolCalls[0].Name)
	}
	if toolCalls[0].ID != "call_1" {
		t.Errorf("tool id = %s", toolCalls[0].ID)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(toolCalls[0].Arguments), &args); err != nil {
		t.Fatalf("arguments not JSON: %v", err)
	}
	if args["path"] != "/tmp/a.go" {
		t.Errorf("args = %v", args)
	}
}

// ═══════════════ 非流式（无工具循环的简单回答） ═══════════════

func TestStream_Should_HandleNonStreamResponse(t *testing.T) {
	// 有些兼容服务忽略 stream 参数返回完整 JSON——应该能处理
	srv := mockStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"x","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"OK"},"index":0}]}`)
	})

	cl := &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	var text strings.Builder
	err := cl.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(p StreamPart) error {
		if p.Type == PartText {
			text.WriteString(p.Text)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.String() != "OK" {
		t.Errorf("text = %q, want OK", text.String())
	}
}

// ═══════════════ 错误处理 ═══════════════

func TestStream_Should_ReturnError_OnHTTPError(t *testing.T) {
	srv := mockStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	})

	cl := &Client{BaseURL: srv.URL, APIKey: "bad", Model: "m"}
	err := cl.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(p StreamPart) error { return nil })
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestStream_Should_ReturnError_OnMalformedSSE(t *testing.T) {
	srv := mockStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {invalid json}`)
	})

	cl := &Client{BaseURL: srv.URL, APIKey: "k", Model: "m"}
	err := cl.Stream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(p StreamPart) error { return nil })
	if err == nil {
		t.Fatal("expected error on malformed SSE")
	}
}

// ═══════════════ Token 估算 ═══════════════

func TestCountTokens_Should_EstimateByRoughHeuristic(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
		want int
	}{
		{
			name: "empty",
			msgs: nil,
			want: 0,
		},
		{
			name: "single short",
			msgs: []Message{{Role: "user", Content: "hello"}},
			want: 1,
		},
		{
			name: "chinese text counts by runes",
			msgs: []Message{{Role: "user", Content: "你好世界"}},
			want: 4,
		},
		{
			name: "multiple messages summed",
			msgs: []Message{
				{Role: "system", Content: "你是一个助手"},
				{Role: "user", Content: "hello world"},
			},
			want: 8,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountTokens(tt.msgs)
			if got != tt.want {
				t.Errorf("CountTokens = %d, want %d", got, tt.want)
			}
		})
	}
}
