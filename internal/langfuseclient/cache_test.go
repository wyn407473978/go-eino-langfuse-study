package langfuseclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPromptCacheConcurrentSameKey(t *testing.T) {
	// 1. 统计“远端”真正收到多少次请求。
	var requests atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"name": "service-assistant",
				"version": 1,
				"type": "text",
				"prompt": "你是办事助手。",
				"labels": ["production"]
			}`)
		},
	))
	defer server.Close()

	// 2. 客户端连接模拟服务；所有并发请求共用一个缓存。
	client, err := NewClient(server.URL, "test-public", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	cache, err := NewPromptCache(client, time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 3. 收集每个 goroutine 的执行结果。
	type outcome struct {
		result *PromptResult
		err    error
	}

	const workers = 10
	results := make(chan outcome, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		go func() {
			// 等待统一发令，再开始获取提示词。
			<-start

			result, err := cache.GetTextPrompt(
				ctx, "service-assistant", "production",
			)
			results <- outcome{result: result, err: err}
		}()
	}

	// 关闭 channel 会让所有等待接收的 goroutine 继续执行。
	close(start)

	// 4. 收齐十次结果，在测试主 goroutine 中做断言。
	var remoteCount, cacheCount int

	for i := 0; i < workers; i++ {
		got := <-results
		if got.err != nil {
			t.Errorf("获取提示词失败：%v", got.err)
			continue
		}
		if got.result == nil || got.result.Prompt == nil {
			t.Error("返回了空结果")
			continue
		}

		switch got.result.Source {
		case "remote":
			remoteCount++
		case "cache":
			cacheCount++
		default:
			t.Errorf("未知来源：%q", got.result.Source)
		}
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("远端请求次数 = %d，期望 1", got)
	}
	if remoteCount != 1 || cacheCount != workers-1 {
		t.Errorf(
			"remote=%d，cache=%d；期望 remote=1，cache=%d",
			remoteCount, cacheCount, workers-1,
		)
	}

	t.Logf(
		"远端请求=%d，remote=%d，cache=%d",
		requests.Load(), remoteCount, cacheCount,
	)
}

func TestPromptCacheDifferentKeysRunIndependently(t *testing.T) {
	// 通知测试：production 已经到达远端。
	productionStarted := make(chan struct{})

	// 测试发出信号后，才允许 production 返回。
	releaseProduction := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			label := r.URL.Query().Get("label")

			if label == "production" {
				close(productionStarted)

				select {
				case <-releaseProduction:
					// 收到放行信号，继续返回响应。
				case <-r.Context().Done():
					return
				}
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"name": "service-assistant",
				"version": 1,
				"type": "text",
				"prompt": "你是办事助手。"
			}`)
		},
	))
	defer server.Close()

	// 即使测试中途失败，也让被暂停的 handler 能退出。
	defer close(releaseProduction)

	client, err := NewClient(server.URL, "test-public", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	cache, err := NewPromptCache(client, time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. 后台启动 production 请求。
	productionDone := make(chan error, 1)

	go func() {
		_, err := cache.GetTextPrompt(
			ctx, "service-assistant", "production",
		)
		productionDone <- err
	}()

	// 2. 确保 production 已到达远端，而且正被暂停。
	select {
	case <-productionStarted:
		t.Log("production 已到达远端，暂不放行")
	case <-ctx.Done():
		t.Fatal("等待 production 到达远端超时")
	}

	// 3. 此时 production 尚未返回，获取另一个 key。
	result, err := cache.GetTextPrompt(
		ctx, "service-assistant", "staging",
	)
	if err != nil {
		t.Fatalf("production 暂停期间，staging 获取失败：%v", err)
	}
	if result == nil || result.Source != "remote" {
		t.Fatalf("staging 首次获取应来自 remote，实际：%+v", result)
	}

	t.Log("production 尚未放行，staging 已成功返回")

	// 4. staging 已完成，现在才放行 production。
	releaseProduction <- struct{}{}

	select {
	case err := <-productionDone:
		if err != nil {
			t.Fatalf("production 获取失败：%v", err)
		}
	case <-ctx.Done():
		t.Fatal("放行后，production 未及时完成")
	}

	t.Log("production 放行后也正常完成")
}

func TestPromptCacheWaiterTimeout(t *testing.T) {
	var requests atomic.Int64

	started := make(chan struct{}, 1)
	release := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)

			// 通知测试：请求已经进入远端。
			select {
			case started <- struct{}{}:
			default:
			}

			// 暂停响应，直到测试放行或请求被取消。
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{
				"name": "service-assistant",
				"version": 1,
				"type": "text",
				"prompt": "你是办事助手。"
			}`)
		},
	))
	defer server.Close()
	defer close(release)

	client, err := NewClient(server.URL, "test-public", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	cache, err := NewPromptCache(client, time.Minute, 0)
	if err != nil {
		t.Fatal(err)
	}

	// 整个测试的时间上限。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 1. A 取得锁，进入远端请求。
	firstDone := make(chan error, 1)

	go func() {
		_, err := cache.GetTextPrompt(
			ctx, "service-assistant", "production",
		)
		firstDone <- err
	}()

	select {
	case <-started:
		t.Log("A 已到达远端，暂不放行")
	case <-ctx.Done():
		t.Fatal("等待 A 到达远端超时")
	}

	// 2. B 获取相同 key，但只愿意等待 100ms。
	waitCtx, cancelWait := context.WithTimeout(ctx, 100*time.Millisecond)
	result, err := cache.GetTextPrompt(
		waitCtx, "service-assistant", "production",
	)
	cancelWait()

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("B 应超时退出，实际错误：%v", err)
	}
	if result != nil {
		t.Fatal("B 超时后不应返回提示词")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("B 不应进入远端，实际请求次数：%d", got)
	}

	t.Log("B 等待超时退出，远端仍只有 A 的一次请求")

	// 3. B 已退出，现在放行 A。
	release <- struct{}{}

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("B 超时不应影响 A，实际错误：%v", err)
		}
	case <-ctx.Done():
		t.Fatal("放行后 A 未完成，可能没有正确释放锁")
	}

	// 4. C 应能取得锁，并命中 A 保存的缓存。
	result, err = cache.GetTextPrompt(
		ctx, "service-assistant", "production",
	)
	if err != nil {
		t.Fatalf("C 获取失败：%v", err)
	}
	if result == nil || result.Source != "cache" {
		t.Fatalf("C 应命中缓存，实际：%+v", result)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("最终远端请求次数应为 1，实际：%d", got)
	}

	t.Log("A 正常完成，C 命中缓存，远端总请求次数为 1")
}
