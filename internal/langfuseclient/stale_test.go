package langfuseclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPromptCacheStalePolicy(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		age       time.Duration
		maxStale  time.Duration
		cached    bool
		cancelled bool
		wantStale bool
	}{
		{"503_within_window", 503, time.Minute, time.Hour, true, false, true},
		{"429_within_window", 429, time.Minute, time.Hour, true, false, true},
		{"401_rejected", 401, time.Minute, time.Hour, true, false, false},
		{"404_rejected", 404, time.Minute, time.Hour, true, false, false},
		{"too_old", 503, 2 * time.Hour, time.Hour, true, false, false},
		{"disabled", 503, time.Minute, 0, true, false, false},
		{"no_cached_prompt", 503, time.Minute, time.Hour, false, false, false},
		{"cancelled", 503, time.Minute, time.Hour, true, true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			client, err := NewClient(srv.URL, "test-public", "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			cache, err := NewPromptCache(client, time.Minute, tt.maxStale)
			if err != nil {
				t.Fatal(err)
			}
			key := promptKey{Name: "service-assistant", Label: "production"}
			expiry := time.Now().Add(-tt.age)
			if tt.cached {
				cache.entries[key] = cacheEntry{
					Prompt:    &TextPrompt{Name: key.Name, Version: 7, Type: "text", Prompt: "old prompt", Labels: []string{"production"}},
					ExpiresAt: expiry,
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			got, err := cache.GetTextPrompt(ctx, key.Name, key.Label)
			if !tt.wantStale {
				if err == nil || got != nil {
					t.Fatalf("expected nil result and error, got %+v, %v", got, err)
				}
				if tt.cancelled {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancelled, got %v", err)
					}
				} else {
					var statusErr *HTTPStatusError
					if !errors.As(err, &statusErr) || statusErr.StatusCode != tt.status {
						t.Fatalf("wrong error: %v", err)
					}
				}
				return
			}
			if err != nil || got == nil {
				t.Fatalf("expected stale result, got %+v, %v", got, err)
			}
			if got.Source != "stale" || got.Prompt.Version != 7 || got.Prompt.Prompt != "old prompt" {
				t.Fatalf("wrong stale result: %+v", got)
			}
			var statusErr *HTTPStatusError
			if !errors.As(got.RefreshError, &statusErr) || statusErr.StatusCode != tt.status {
				t.Fatalf("wrong refresh error: %v", got.RefreshError)
			}
			got.Prompt.Prompt = "changed"
			got.Prompt.Labels[0] = "changed"
			cache.mu.Lock()
			saved := cache.entries[key]
			cache.mu.Unlock()
			if !saved.ExpiresAt.Equal(expiry) || saved.Prompt.Prompt != "old prompt" || saved.Prompt.Labels[0] != "production" {
				t.Fatalf("stale result changed cached data or expiry: %+v", saved)
			}
		})
	}
}

func TestPromptCacheStaleRecovery(t *testing.T) {
	var status atomic.Int64
	status.Store(503)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status.Load() == 503 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"service-assistant","version":8,"type":"text","prompt":"new prompt","labels":["production"]}`))
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-public", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewPromptCache(client, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := promptKey{Name: "service-assistant", Label: "production"}
	cache.entries[key] = cacheEntry{Prompt: &TextPrompt{Name: key.Name, Version: 7, Type: "text", Prompt: "old prompt"}, ExpiresAt: time.Now().Add(-time.Minute)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	old, err := cache.GetTextPrompt(ctx, key.Name, key.Label)
	if err != nil || old == nil || old.Source != "stale" {
		t.Fatalf("stale failed: %+v %v", old, err)
	}
	status.Store(200)
	fresh, err := cache.GetTextPrompt(ctx, key.Name, key.Label)
	if err != nil || fresh == nil || fresh.Source != "remote" || fresh.RefreshError != nil || fresh.Prompt.Version != 8 {
		t.Fatalf("recovery failed: %+v %v", fresh, err)
	}
	hit, err := cache.GetTextPrompt(ctx, key.Name, key.Label)
	if err != nil || hit == nil || hit.Source != "cache" || hit.RefreshError != nil || hit.Prompt.Version != 8 {
		t.Fatalf("cache after recovery failed: %+v %v", hit, err)
	}
}
