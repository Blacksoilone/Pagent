// Package provider 实现 OpenAI-compatible 模型层。
//
// 设计依据：docs/agent-workspace-product-draft.md 7.1 模型/API 层。
// 只支持 OpenAI 兼容接口（DeepSeek/OpenRouter/Ollama 等），
// 只抽象"对话流"这一个动作：Stream + CountTokens。
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 对话消息（OpenAI 兼容格式）。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ReasoningContent 思考内容（DeepSeek 思考模式：回传时必须带上，4.6 视为不可信数据）
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ToolCalls 用于 assistant 消息（结构化工具调用，OpenAI 协议）
	ToolCalls []MessageToolCall `json:"tool_calls,omitempty"`
	// ToolCallID 用于 tool 角色消息（工具结果关联）
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// MessageToolCall assistant 消息中的工具调用。
type MessageToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc 工具调用的函数部分。
type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall 工具调用请求。
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON 参数
}

// StreamPart 流式输出的一部分（3.12 会话运行时的 part）。
type StreamPart struct {
	Type     PartType
	Text     string
	ToolCall ToolCall
}

// PartType 流式 part 类型。
type PartType int

const (
	PartText PartType = iota
	PartToolCall
	PartReasoning
)

// Client OpenAI-compatible 客户端。
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// New 创建客户端。baseURL 形如 https://api.deepseek.com/v1。
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		Timeout: 120 * time.Second,
	}
}

// ToolDef 工具声明（OpenAI function calling schema）。
type ToolDef struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef 工具函数定义。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// chatRequest OpenAI chat completions 请求体。
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

// chatChunk SSE 流中的一个块。
type chatChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Choices []struct {
		Delta struct {
			Role             string `json:"role"`
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// Stream 调用模型并流式回调 part。回调返回 error 时终止流。
// tools 声明模型可用的工具（可为空，此时模型不会调用工具）。
func (c *Client) Stream(ctx context.Context, msgs []Message, tools []ToolDef, onPart func(StreamPart) error) error {
	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	timeout := c.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("provider error %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return c.consumeSSE(resp.Body, onPart)
	}
	return c.consumeJSON(resp.Body, onPart)
}

// consumeSSE 解析 Server-Sent Events 流。
func (c *Client) consumeSSE(r io.Reader, onPart func(StreamPart) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// 工具调用参数跨多个 chunk 累积（OpenAI 流式协议）。
	// 用 slice 而非 map 保证冲刷顺序 = 工具 index 顺序。
	type accToolCall struct {
		index    int
		id, name string
		args     strings.Builder
	}
	var pending []accToolCall
	pendingIdx := make(map[int]int) // tool index → pending 中的位置

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("parse SSE chunk: %w", err)
		}
		for _, ch := range chunk.Choices {
			// 非流式兼容分支（message 字段）
			if ch.Message.Role != "" || ch.Message.Content != "" || len(ch.Message.ToolCalls) > 0 {
				if ch.Message.Content != "" {
					if err := onPart(StreamPart{Type: PartText, Text: ch.Message.Content}); err != nil {
						return err
					}
				}
				for _, tc := range ch.Message.ToolCalls {
					if err := onPart(StreamPart{Type: PartToolCall, ToolCall: ToolCall{
						ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
					}}); err != nil {
						return err
					}
				}
				continue
			}
			// 流式 delta
			if ch.Delta.Content != "" {
				if err := onPart(StreamPart{Type: PartText, Text: ch.Delta.Content}); err != nil {
					return err
				}
			}
			if ch.Delta.ReasoningContent != "" {
				// 思考内容：不展示，只记录（供回传）
				if err := onPart(StreamPart{Type: PartReasoning, Text: ch.Delta.ReasoningContent}); err != nil {
					return err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				pos, ok := pendingIdx[tc.Index]
				if !ok {
					pos = len(pending)
					pendingIdx[tc.Index] = pos
					pending = append(pending, accToolCall{index: tc.Index, id: tc.ID, name: tc.Function.Name})
				}
				acc := &pending[pos]
				if tc.Function.Arguments != "" {
					acc.args.WriteString(tc.Function.Arguments)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// 流中断：仍冲刷已累积完成的工具调用（保证已接收的参数不丢）
		for _, acc := range pending {
			if acc.name == "" {
				continue
			}
			if err := onPart(StreamPart{Type: PartToolCall, ToolCall: ToolCall{
				ID: acc.id, Name: acc.name, Arguments: acc.args.String(),
			}}); err != nil {
				return err
			}
		}
		return fmt.Errorf("read SSE: %w", err)
	}
	for _, acc := range pending {
		if acc.name == "" {
			continue
		}
		if err := onPart(StreamPart{Type: PartToolCall, ToolCall: ToolCall{
			ID: acc.id, Name: acc.name, Arguments: acc.args.String(),
		}}); err != nil {
			return err
		}
	}
	return nil
}

// consumeJSON 处理兼容服务返回的完整 JSON（非流式）。
func (c *Client) consumeJSON(r io.Reader, onPart func(StreamPart) error) error {
	var resp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return fmt.Errorf("parse JSON response: %w", err)
	}
	for _, ch := range resp.Choices {
		if ch.Message.Content != "" {
			if err := onPart(StreamPart{Type: PartText, Text: ch.Message.Content}); err != nil {
				return err
			}
		}
		for _, tc := range ch.Message.ToolCalls {
			if err := onPart(StreamPart{Type: PartToolCall, ToolCall: ToolCall{
				ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments,
			}}); err != nil {
				return err
			}
		}
	}
	return nil
}

// CountTokens 估算消息 token 数（7.1 Token 管理）。
func (c *Client) CountTokens(msgs []Message) int {
	return CountTokens(msgs)
}

// ErrEmptyResponse 模型没有返回任何内容。
var ErrEmptyResponse = errors.New("empty response from provider")

// CountTokens 估算消息的 token 数（启发式：英文按 4 字符 1 token，中文按 1 字符 1 token）。
// 设计依据：7.1 Token 管理（请求计数触发压缩）。
// 注意：这是估算，不是精确计数；精确计数由模型 API 提供（未来）。
func CountTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += estimate(m.Content)
	}
	return total
}

func estimate(s string) int {
	if s == "" {
		return 0
	}
	cjk := 0
	ascii := 0
	for _, r := range s {
		if r > 0x2E80 { // CJK 及以后
			cjk++
		} else {
			ascii++
		}
	}
	t := cjk + ascii/4
	if t == 0 {
		return 1
	}
	return t
}
