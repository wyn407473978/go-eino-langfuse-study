package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/gin-gonic/gin"
)

func TestChatHandler(t *testing.T) {
	validBody := `{"message":"  你好  ","user_id":"user-001","session_id":"session-001"}`
	tests := []struct {
		name       string
		body       string
		modelError error
		wantStatus int
		wantCalls  int
	}{
		{name: "成功", body: validBody, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "非法JSON", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "缺少问题", body: `{"user_id":"u","session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "缺少用户", body: `{"message":"你好","session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "缺少会话", body: `{"message":"你好","user_id":"u"}`, wantStatus: http.StatusBadRequest},
		{name: "空白问题", body: `{"message":" \t ","user_id":"u","session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "空白用户", body: `{"message":"你好","user_id":" ","session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "空白会话", body: `{"message":"你好","user_id":"u","session_id":" "}`, wantStatus: http.StatusBadRequest},
		{name: "错误ID类型", body: `{"message":"你好","user_id":1,"session_id":"s"}`, wantStatus: http.StatusBadRequest},
		{name: "模型失败", body: validBody, modelError: errors.New("private upstream failure"), wantStatus: http.StatusBadGateway, wantCalls: 1},
		{name: "包装的超时错误", body: validBody, modelError: fmt.Errorf("upstream: %w", context.DeadlineExceeded), wantStatus: http.StatusGatewayTimeout, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			router := gin.New()
			router.POST("/chat", newChatHandler(func(ctx context.Context, message string) (string, error) {
				calls++
				if message != "你好" {
					t.Errorf("传给模型的问题 = %q", message)
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
	router.POST("/chat", newChatHandler(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}, time.Millisecond))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat",
		strings.NewReader(`{"message":"你好","user_id":"u","session_id":"s"}`)))
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("状态码 = %d，期望504", response.Code)
	}
}

func TestChatHandlerPropagatesCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	var callContext context.Context
	router := gin.New()
	router.POST("/chat", newChatHandler(func(ctx context.Context, _ string) (string, error) {
		callContext = ctx
		return "", ctx.Err()
	}, time.Second))
	request := httptest.NewRequest(http.MethodPost, "/chat",
		strings.NewReader(`{"message":"你好","user_id":"u","session_id":"s"}`)).WithContext(parent)
	router.ServeHTTP(httptest.NewRecorder(), request)
	if callContext == nil || !errors.Is(callContext.Err(), context.Canceled) {
		t.Fatal("请求取消没有传递给模型调用")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestModelChatMessages(t *testing.T) {
	// 用内存中的HTTP响应验证真实适配器，不连接模型供应商。
	var capturedMessages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		Model:   "test-model",
		APIKey:  "test-key",
		BaseURL: "https://model.invalid",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var payload struct {
				Messages json.RawMessage `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(payload.Messages, &capturedMessages); err != nil {
				return nil, err
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"test","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"测试回答"},"finish_reason":"stop"}]}`)),
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, question := range []string{"问题一", "问题二"} {
		answer, err := ModelChat(context.Background(), chatModel, question)
		if err != nil || answer != "测试回答" {
			t.Fatalf("回答 = %q，错误 = %v", answer, err)
		}
		if len(capturedMessages) != 2 || capturedMessages[0].Role != "system" || capturedMessages[0].Content == "" || capturedMessages[1].Role != "user" || capturedMessages[1].Content != question {
			t.Fatalf("消息缺少system/user或混入前一次调用: %+v", capturedMessages)
		}
	}
}
