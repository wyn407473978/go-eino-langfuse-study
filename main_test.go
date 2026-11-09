package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"go-langfuse-demo/internal/memory"
	"go-langfuse-demo/model/req"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

func TestChatHandler(t *testing.T) {
	validBody := `{"message":"  你好  ","userId":"user-001","sessionId":"session-001"}`
	tests := []struct {
		name       string
		body       string
		modelError error
		wantStatus int
		wantCalls  int
	}{
		{name: "成功", body: validBody, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "非法JSON", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "缺少问题", body: `{"userId":"u","sessionId":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "缺少用户", body: `{"message":"你好","sessionId":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "缺少会话", body: `{"message":"你好","userId":"u"}`, wantStatus: http.StatusBadRequest},
		{name: "空白问题", body: `{"message":" \t ","userId":"u","sessionId":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "空白用户", body: `{"message":"你好","userId":" ","sessionId":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "空白会话", body: `{"message":"你好","userId":"u","sessionId":" "}`, wantStatus: http.StatusBadRequest},
		{name: "错误ID类型", body: `{"message":"你好","userId":1,"sessionId":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "旧字段名不匹配", body: `{"message":"你好","user_id":"u","session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "模型失败", body: validBody, modelError: errors.New("private upstream failure"), wantStatus: http.StatusBadGateway, wantCalls: 1},
		{name: "包装的超时错误", body: validBody, modelError: fmt.Errorf("upstream: %w", context.DeadlineExceeded), wantStatus: http.StatusGatewayTimeout, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			router := gin.New()
			router.POST("/chat", newChatHandler(func(ctx context.Context, chatReq req.ModelChatReq) (string, error) {
				calls++
				if chatReq.Message != "你好" {
					t.Errorf("传给模型的问题 = %q", chatReq.Message)
				}
				if chatReq.UserID != "user-001" || chatReq.SessionID != "session-001" {
					t.Errorf("用户或会话ID没有正确传递: %+v", chatReq)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("模型调用没有截止时间")
				}
				return "你好，小林", tt.modelError
			}, time.Second))
			request := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d；响应 = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if calls != tt.wantCalls {
				t.Errorf("模型调用次数 = %d，期望 %d", calls, tt.wantCalls)
			}
			var body map[string]string
			decoder := json.NewDecoder(response.Body)
			if err := decoder.Decode(&body); err != nil {
				t.Fatalf("响应不是JSON对象: %v", err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("响应包含额外数据: %v", err)
			}
			if tt.wantStatus == http.StatusOK {
				if body["answer"] != "你好，小林" {
					t.Errorf("回答 = %q", body["answer"])
				}
			} else if len(body) != 1 || body["error"] == "" || strings.Contains(body["error"], "private upstream") {
				t.Errorf("错误响应不符合约定: %v", body)
			}
		})
	}
}

func TestChatHandlerDeadline(t *testing.T) {
	router := gin.New()
	router.POST("/chat", newChatHandler(func(ctx context.Context, _ req.ModelChatReq) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}, time.Millisecond))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat",
		strings.NewReader(`{"message":"你好","userId":"u","sessionId":"s"}`)))
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("状态码 = %d，期望504", response.Code)
	}
}

func TestChatHandlerPropagatesCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	var callContext context.Context
	router := gin.New()
	router.POST("/chat", newChatHandler(func(ctx context.Context, _ req.ModelChatReq) (string, error) {
		callContext = ctx
		return "", ctx.Err()
	}, time.Second))
	request := httptest.NewRequest(http.MethodPost, "/chat",
		strings.NewReader(`{"message":"你好","userId":"u","sessionId":"s"}`)).WithContext(parent)
	router.ServeHTTP(httptest.NewRecorder(), request)
	if callContext == nil || !errors.Is(callContext.Err(), context.Canceled) {
		t.Fatal("请求取消没有传递给模型调用")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// 测试只替换HTTP传输层，仍执行真实适配器的请求编码和响应解析。
func newTestChatModel(t *testing.T, transport roundTripFunc) *openai.ChatModel {
	t.Helper()
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		Model:      "test-model",
		APIKey:     "test-key",
		BaseURL:    "https://model.invalid",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return chatModel
}

type modelMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func TestModelChatMessages(t *testing.T) {
	var capturedMessages []modelMessage
	calls := 0
	chatModel := newTestChatModel(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var payload struct {
			Messages []modelMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		capturedMessages = payload.Messages
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"test","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"测试回答"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)),
		}, nil
	})
	store := memory.NewStore()
	tests := []struct {
		name         string
		chatReq      req.ModelChatReq
		wantMessages []modelMessage // 不包含首条system消息。
	}{
		{
			name:         "首次请求",
			chatReq:      req.ModelChatReq{Message: "我叫王源", UserID: "u1", SessionID: "s1"},
			wantMessages: []modelMessage{{Role: "user", Content: "我叫王源"}},
		},
		{
			name:    "同用户同会话携带完整历史",
			chatReq: req.ModelChatReq{Message: "我叫什么", UserID: "u1", SessionID: "s1"},
			wantMessages: []modelMessage{
				{Role: "user", Content: "我叫王源"},
				{Role: "assistant", Content: "测试回答"},
				{Role: "user", Content: "我叫什么"},
			},
		},
		{
			name:         "同用户不同会话隔离",
			chatReq:      req.ModelChatReq{Message: "其他会话的问题", UserID: "u1", SessionID: "s2"},
			wantMessages: []modelMessage{{Role: "user", Content: "其他会话的问题"}},
		},
		{
			name:         "不同用户相同会话ID隔离",
			chatReq:      req.ModelChatReq{Message: "其他用户的问题", UserID: "u2", SessionID: "s1"},
			wantMessages: []modelMessage{{Role: "user", Content: "其他用户的问题"}},
		},
		{
			name:    "返回原会话历史仍完整",
			chatReq: req.ModelChatReq{Message: "继续聊天", UserID: "u1", SessionID: "s1"},
			wantMessages: []modelMessage{
				{Role: "user", Content: "我叫王源"},
				{Role: "assistant", Content: "测试回答"},
				{Role: "user", Content: "我叫什么"},
				{Role: "assistant", Content: "测试回答"},
				{Role: "user", Content: "继续聊天"},
			},
		},
	}
	// 这些子测试按顺序共享Store，模拟同一个运行中的服务，不能使用t.Parallel。
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previousCalls := calls
			answer, err := ModelChat(context.Background(), chatModel, tt.chatReq, store)
			if err != nil || answer != "测试回答" {
				t.Fatalf("回答 = %q，错误 = %v", answer, err)
			}
			if calls != previousCalls+1 {
				t.Fatalf("模型调用次数增加了 %d，期望 1", calls-previousCalls)
			}
			if len(capturedMessages) != len(tt.wantMessages)+1 {
				t.Fatalf("模型输入消息数量 = %d，期望 %d", len(capturedMessages), len(tt.wantMessages)+1)
			}
			if capturedMessages[0].Role != "system" || capturedMessages[0].Content == "" {
				t.Errorf("首条消息不是有效的system消息: %+v", capturedMessages[0])
			}
			if !reflect.DeepEqual(capturedMessages[1:], tt.wantMessages) {
				t.Errorf("模型输入 = %+v，期望 = %+v", capturedMessages[1:], tt.wantMessages)
			}

			// 存储里只应有历史user/assistant，本次成功回答也应已保存。
			history := store.Load(tt.chatReq.UserID, tt.chatReq.SessionID)
			wantHistory := append([]modelMessage{}, tt.wantMessages...)
			wantHistory = append(wantHistory, modelMessage{Role: "assistant", Content: "测试回答"})
			if len(history) != len(wantHistory) {
				t.Fatalf("历史消息数量 = %d，期望 %d", len(history), len(wantHistory))
			}
			for i, want := range wantHistory {
				if history[i] == nil || string(history[i].Role) != want.Role || history[i].Content != want.Content {
					t.Errorf("历史第%d条 = %+v，期望 = %+v", i+1, history[i], want)
				}
			}
			last := history[len(history)-1]
			if last == nil || last.ResponseMeta == nil || last.ResponseMeta.Usage == nil || last.ResponseMeta.Usage.TotalTokens != 5 {
				t.Error("存储没有保留模型返回消息的usage元数据")
			}
		})
	}
}

func TestModelChatFailureLeavesHistoryUnchanged(t *testing.T) {
	tests := []struct {
		name      string
		callError error
	}{
		{name: "认证失败"},
		{name: "超时", callError: context.DeadlineExceeded},
		{name: "取消", callError: context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chatModel := newTestChatModel(t, func(r *http.Request) (*http.Response, error) {
				if tt.callError != nil {
					return nil, tt.callError
				}
				return &http.Response{
					StatusCode: http.StatusUnauthorized,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid test key","type":"authentication_error","code":"invalid_api_key"}}`)),
				}, nil
			})
			store := memory.NewStore()
			store.AppendTurn("u", "s", schema.UserMessage("之前的问题"), schema.AssistantMessage("之前的回答", nil))
			before := store.Load("u", "s")
			answer, err := ModelChat(context.Background(), chatModel,
				req.ModelChatReq{Message: "这次失败的问题", UserID: "u", SessionID: "s"}, store)
			if err == nil || answer != "" {
				t.Fatalf("失败时回答 = %q，错误 = %v", answer, err)
			}
			if tt.callError != nil && !errors.Is(err, tt.callError) {
				t.Errorf("错误链丢失: %v", err)
			}
			if after := store.Load("u", "s"); !reflect.DeepEqual(after, before) {
				t.Fatal("模型调用失败后原有历史发生变化")
			}
		})
	}
}
