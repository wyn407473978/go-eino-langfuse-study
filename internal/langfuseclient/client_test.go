package langfuseclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTextPromptHTTPStatusError(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"unauthorized", http.StatusUnauthorized},
		{"unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 模拟远端，直接返回本轮指定的状态码。
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.status)
				},
			))
			defer server.Close()

			client, err := NewClient(
				server.URL, "test-public", "test-secret",
			)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(
				context.Background(), time.Second,
			)
			defer cancel()

			prompt, err := client.GetTextPrompt(
				ctx, "service-assistant", "production",
			)

			if prompt != nil {
				t.Fatal("远端返回错误时，不应返回提示词")
			}

			var statusErr *HTTPStatusError
			if !errors.As(err, &statusErr) {
				t.Fatalf(
					"期望 *HTTPStatusError，实际类型=%T，错误=%v",
					err, err,
				)
			}

			if statusErr.StatusCode != tt.status {
				t.Fatalf(
					"状态码=%d，期望=%d",
					statusErr.StatusCode, tt.status,
				)
			}

			t.Logf("成功识别 HTTP 状态码：%d", statusErr.StatusCode)
		})
	}
}
