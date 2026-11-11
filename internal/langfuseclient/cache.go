package langfuseclient

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type promptKey struct {
	Name  string
	Label string
}

type cacheEntry struct {
	Prompt    *TextPrompt
	ExpiresAt time.Time
}

type PromptResult struct {
	Prompt       *TextPrompt
	Source       string // Source string // remote、cache、stale、fallback
	RefreshError error  // 使用旧缓存时，记录本次刷新失败原因
}

type PromptCache struct {
	mu       sync.Mutex
	locks    map[promptKey]chan struct{}
	client   *Client
	ttl      time.Duration
	entries  map[promptKey]cacheEntry
	maxStale time.Duration
}

func NewPromptCache(
	client *Client,
	ttl time.Duration,
	maxStale time.Duration,
) (*PromptCache, error) {
	if client == nil {
		return nil, errors.New("client is nil")
	}
	if ttl <= 0 {
		return nil, errors.New("ttl is negative")
	}
	if maxStale < 0 {
		return nil, errors.New("maxStale is negative")
	}
	promptCache := new(PromptCache)
	promptCache.client = client
	promptCache.ttl = ttl
	promptCache.entries = make(map[promptKey]cacheEntry)
	promptCache.locks = make(map[promptKey]chan struct{}, 1)
	promptCache.maxStale = maxStale
	return promptCache, nil
}

func (c *PromptCache) GetTextPrompt(
	ctx context.Context,
	name, label string,
) (*PromptResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(label) == "" {
		return nil, errors.New("invalid prompt name or label")
	}
	key := promptKey{Name: name, Label: label}
	unlock, err := c.lockPrompt(ctx, key)
	if err != nil {
		return nil, err
	}
	defer unlock()

	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.ExpiresAt) {
		return &PromptResult{
			Prompt: cloneTextPrompt(entry.Prompt),
			Source: "cache",
		}, nil
	}
	// 没命中或已过期，交给原来的 HTTP 客户端获取。
	prompt, err := c.client.GetTextPrompt(ctx, name, label)
	if err != nil {
		// 调用方已经取消或超时，就结束本次调用。
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		// 必须有旧缓存，且故障类型和旧缓存年龄都符合要求。
		if ok && c.maxStale > 0 && isTemporaryPromptError(err) && time.Now().Before(entry.ExpiresAt.Add(c.maxStale)) {
			return &PromptResult{
				Prompt:       cloneTextPrompt(entry.Prompt),
				Source:       "stale",
				RefreshError: err,
			}, nil
		}

		return nil, err
	} // 获取成功后保存一份副本，并从此刻开始计算有效期。
	newEntry := cacheEntry{
		Prompt:    cloneTextPrompt(prompt),
		ExpiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Lock()
	c.entries[key] = newEntry
	c.mu.Unlock()

	// 再给调用方一份独立副本，标明本次来自远程。
	return &PromptResult{
		Prompt: cloneTextPrompt(prompt),
		Source: "remote",
	}, nil
}

func cloneTextPrompt(prompt *TextPrompt) *TextPrompt {
	copied := *prompt
	copied.Labels = append([]string(nil), prompt.Labels...)
	return &copied
}

func (c *PromptCache) lockPrompt(
	ctx context.Context,
	key promptKey,
) (func(), error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	c.mu.Lock()
	ch, ok := c.locks[key]
	if !ok {
		ch = make(chan struct{}, 1)
		c.locks[key] = ch
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case ch <- struct{}{}:
		// 取消与获取可能同时发生，因此取得后再次检查。
		if err := ctx.Err(); err != nil {
			<-ch
			return nil, err
		}

		// 返回解锁函数，调用它时取出占位符。
		return func() {
			<-ch
		}, nil
	}

}
