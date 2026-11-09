package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go-langfuse-demo/internal/memory"
	"go-langfuse-demo/model/req"
)

type chatWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *chatWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func successfulChatResponse(answer string) *http.Response {
	// answer来自下面固定的测试文本；%q将它写为带引号的字符串。
	body := fmt.Sprintf(`{"id":"test","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}]}`, answer)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestModelChatSerializesCompleteTurns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := memory.NewStore()
	firstEntered := make(chan struct{})
	continueFirst := make(chan struct{})
	requests := make(chan []modelMessage, 2)
	chatModel := newTestChatModel(t, func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Messages []modelMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if len(payload.Messages) == 0 {
			return nil, errors.New("模型输入为空")
		}
		requests <- payload.Messages
		question := payload.Messages[len(payload.Messages)-1].Content
		if question == "第一轮" {
			close(firstEntered)
			// 保持第一轮尚未结束，安排第二轮在此期间开始。
			select {
			case <-continueFirst:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return successfulChatResponse("回答：" + question), nil
	})
	type chatResult struct {
		answer string
		err    error
	}
	firstResult := make(chan chatResult, 1)
	go func() {
		answer, err := ModelChat(ctx, chatModel,
			req.ModelChatReq{Message: "第一轮", UserID: "u", SessionID: "s"}, store)
		firstResult <- chatResult{answer: answer, err: err}
	}()
	select {
	case <-firstEntered:
	case <-ctx.Done():
		t.Fatal("第一轮没有进入模型调用")
	}
	waitCtx := &chatWaitContext{Context: ctx, waiting: make(chan struct{})}
	secondResult := make(chan chatResult, 1)
	go func() {
		answer, err := ModelChat(waitCtx, chatModel,
			req.ModelChatReq{Message: "第二轮", UserID: "u", SessionID: "s"}, store)
		secondResult <- chatResult{answer: answer, err: err}
	}()
	select {
	case <-waitCtx.waiting:
	case <-ctx.Done():
		t.Fatal("第二轮没有开始等待")
	}
	close(continueFirst)
	for _, result := range []struct {
		channel <-chan chatResult
		want    string
	}{
		{channel: firstResult, want: "回答：第一轮"},
		{channel: secondResult, want: "回答：第二轮"},
	} {
		select {
		case got := <-result.channel:
			if got.err != nil || got.answer != result.want {
				t.Fatalf("回答 = %q，错误 = %v，期望 %q", got.answer, got.err, result.want)
			}
		case <-ctx.Done():
			t.Fatal("整轮会话未能完成，可能发生循环等待")
		}
	}
	// 此时两轮均已结束，读取记录不会与模型调用并发修改。
	firstInput := <-requests
	secondInput := <-requests
	if len(firstInput) != 2 || firstInput[0].Role != "system" || firstInput[1].Content != "第一轮" {
		t.Fatalf("第一轮输入不正确: %+v", firstInput)
	}
	wantSecond := []modelMessage{
		{Role: "user", Content: "第一轮"},
		{Role: "assistant", Content: "回答：第一轮"},
		{Role: "user", Content: "第二轮"},
	}
	if len(secondInput) != 4 || secondInput[0].Role != "system" || !reflect.DeepEqual(secondInput[1:], wantSecond) {
		t.Fatalf("第二轮没有读到第一轮已完成的历史: %+v", secondInput)
	}
	history := store.Load("u", "s")
	if len(history) != 4 || history[0].Content != "第一轮" || history[1].Content != "回答：第一轮" || history[2].Content != "第二轮" || history[3].Content != "回答：第二轮" {
		t.Fatalf("保存的完整历史不正确: %+v", history)
	}
}

func TestModelChatWaitTimeoutDoesNotCallModel(t *testing.T) {
	store := memory.NewStore()
	unlock, err := store.LockSession(context.Background(), "u", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	calls := 0
	chatModel := newTestChatModel(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return successfulChatResponse("不应执行"), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	answer, err := ModelChat(ctx, chatModel,
		req.ModelChatReq{Message: "等待中的请求", UserID: "u", SessionID: "s"}, store)
	if !errors.Is(err, context.DeadlineExceeded) || answer != "" {
		t.Fatalf("回答 = %q，错误 = %v，期望等待超时", answer, err)
	}
	if calls != 0 {
		t.Errorf("等待失败后仍调用了模型 %d 次", calls)
	}
	if len(store.Load("u", "s")) != 0 {
		t.Error("等待失败改变了历史")
	}
}

func TestModelChatReleasesSessionAfterFailure(t *testing.T) {
	for _, callError := range []error{
		errors.New("模拟模型失败"), context.DeadlineExceeded, context.Canceled,
	} {
		t.Run(callError.Error(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			calls := 0
			chatModel := newTestChatModel(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return nil, callError
				}
				return successfulChatResponse("恢复成功"), nil
			})
			store := memory.NewStore()
			chatReq := req.ModelChatReq{Message: "问题", UserID: "u", SessionID: "s"}
			if _, err := ModelChat(ctx, chatModel, chatReq, store); err == nil {
				t.Fatal("首次模拟失败没有返回错误")
			}
			if len(store.Load("u", "s")) != 0 {
				t.Fatal("失败的第一轮写入了历史")
			}
			// 再次调用同一会话：必须能重新获得锁并成功保存一轮。
			answer, err := ModelChat(ctx, chatModel, chatReq, store)
			if err != nil || answer != "恢复成功" {
				t.Fatalf("失败后会话未恢复: 回答 = %q，错误 = %v", answer, err)
			}
			if len(store.Load("u", "s")) != 2 {
				t.Error("恢复后没有恰好保存一轮消息")
			}
		})
	}
}
