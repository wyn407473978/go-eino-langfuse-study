package langfuseclient

import (
	"errors"
	"fmt"
	"net"
	"net/http"
)

type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf(
		"获取 Prompt 失败，HTTP 状态码：%d",
		e.StatusCode,
	)
}

func isTemporaryPromptError(err error) bool {
	if err == nil {
		return false
	}

	// 本练习允许对 429 和 5xx 使用旧缓存。
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		code := statusErr.StatusCode

		return code == http.StatusTooManyRequests ||
			(code >= 500 && code < 600)
	}

	// 连接失败、读写连接失败等网络操作错误。
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// HTTP 客户端自身的网络超时。
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
