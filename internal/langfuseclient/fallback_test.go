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

func TestPromptCacheFallbackPolicy(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		cancelled  bool
		cached     bool
		expiresIn  time.Duration
		wantSource string
	}{
		{name: "503_without_cache", status: 503, wantSource: "fallback"},
		{name: "429_without_cache", status: 429, wantSource: "fallback"},
		{name: "401_rejected", status: 401},
		{name: "403_rejected", status: 403},
		{name: "404_rejected", status: 404},
		{name: "invalid_json_rejected", status: 200},
		{name: "cancelled_rejected", status: 503, cancelled: true},
		{name: "fresh_cache_preferred", status: 503, cached: true, expiresIn: time.Minute, wantSource: "cache"},
		{name: "stale_cache_preferred", status: 503, cached: true, expiresIn: -time.Minute, wantSource: "stale"},
		{name: "overage_cache_uses_builtin", status: 503, cached: true, expiresIn: -2 * time.Hour, wantSource: "fallback"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tt.status)
				// The 200 case deliberately has no JSON body.
			}))
			defer server.Close()
			client, err := NewClient(server.URL, "test-public", "test-secret")
			if err != nil {
				t.Fatal(err)
			}
			cache, err := NewPromptCache(client, time.Minute, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			key := promptKey{Name: "service-assistant", Label: "production"}
			if tt.cached {
				cache.entries[key] = cacheEntry{
					Prompt:    &TextPrompt{Name: key.Name, Version: 7, Type: "text", Prompt: "cached prompt"},
					ExpiresAt: time.Now().Add(tt.expiresIn),
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if tt.cancelled {
				cancel()
			}
			result, err := cache.GetTextPromptWithFallback(ctx, key.Name, key.Label, "builtin {{service_name}}")
			if tt.wantSource == "" {
				if err == nil || result != nil {
					t.Fatalf("expected nil result and error; got %+v, %v", result, err)
				}
				if tt.cancelled {
					if !errors.Is(err, context.Canceled) || requests.Load() != 0 {
						t.Fatalf("cancelled call made a request or returned wrong error: %d, %v", requests.Load(), err)
					}
				} else if tt.status != 200 {
					var statusErr *HTTPStatusError
					if !errors.As(err, &statusErr) || statusErr.StatusCode != tt.status {
						t.Fatalf("wrong HTTP error: %v", err)
					}
				}
				return
			}
			if err != nil || result == nil || result.Prompt == nil {
				t.Fatalf("expected usable prompt; got %+v, %v", result, err)
			}
			if result.Source != tt.wantSource {
				t.Fatalf("source=%q, want %q", result.Source, tt.wantSource)
			}
			if result.Source == "fallback" {
				if result.Prompt.Name != key.Name || result.Prompt.Version != 0 || result.Prompt.Type != "text" || len(result.Prompt.Labels) != 0 || result.Prompt.Prompt != "builtin {{service_name}}" {
					t.Fatalf("builtin prompt has wrong provenance or content: %+v", result.Prompt)
				}
			} else if result.Prompt.Version != 7 || result.Prompt.Prompt != "cached prompt" {
				t.Fatalf("cached prompt not preserved: %+v", result.Prompt)
			}
			if tt.wantSource == "cache" {
				if result.RefreshError != nil || requests.Load() != 0 {
					t.Fatal("fresh cache should not refresh")
				}
			} else {
				var statusErr *HTTPStatusError
				if !errors.As(result.RefreshError, &statusErr) || statusErr.StatusCode != tt.status {
					t.Fatalf("refresh error not preserved: %v", result.RefreshError)
				}
			}
		})
	}
}

func TestPromptCacheFallbackRecovery(t *testing.T) {
	var status, requests atomic.Int64
	status.Store(503)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if status.Load() == 503 {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"service-assistant","version":9,"type":"text","prompt":"remote prompt","labels":["production"]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test-public", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewPromptCache(client, time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Different builtin values reveal whether the first fallback was cached.
	for _, builtin := range []string{"builtin A", "builtin B"} {
		result, err := cache.GetTextPromptWithFallback(ctx, "service-assistant", "production", builtin)
		if err != nil || result == nil || result.Prompt == nil || result.Source != "fallback" || result.Prompt.Prompt != builtin {
			t.Fatalf("builtin result=%+v, error=%v", result, err)
		}
	}
	status.Store(200)
	for _, wantSource := range []string{"remote", "cache"} {
		result, err := cache.GetTextPromptWithFallback(ctx, "service-assistant", "production", "builtin C")
		if err != nil || result == nil || result.Prompt == nil || result.Source != wantSource || result.Prompt.Version != 9 || result.Prompt.Prompt != "remote prompt" || result.RefreshError != nil {
			t.Fatalf("want source=%s, result=%+v, error=%v", wantSource, result, err)
		}
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("remote requests=%d, want 3", got)
	}
}
