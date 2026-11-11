package observability

import (
	"context"
	"errors"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"strings"
)

type PromptHandler struct {
	*langfuse.CallbackHandler
	promptName    string
	promptVersion int
}

func NewPromptHandler(
	base *langfuse.CallbackHandler,
	promptName string,
	promptVersion int,
) (*PromptHandler, error) {
	promptHandler := new(PromptHandler)
	if base == nil {
		return nil, errors.New("base 参数不能为空")
	}
	if strings.TrimSpace(promptName) == "" {
		return nil, errors.New("prompt name is null")
	}
	if promptVersion <= 0 {
		return nil, errors.New("Prompt 版本必须大于零")
	}
	promptHandler.promptName = promptName
	promptHandler.CallbackHandler = base
	promptHandler.promptVersion = promptVersion
	return promptHandler, nil
}

func (h *PromptHandler) OnStart(
	ctx context.Context,
	info *callbacks.RunInfo,
	input callbacks.CallbackInput,
) context.Context {
	// 先让原 handler 创建本次调用的 span。
	ctx = h.CallbackHandler.OnStart(ctx, info, input)

	if info != nil && info.Component == components.ComponentOfChatModel {
		trace.SpanFromContext(ctx).SetAttributes(
			attribute.String(
				"langfuse.observation.prompt.name",
				h.promptName,
			),
			attribute.Int(
				"langfuse.observation.prompt.version",
				h.promptVersion,
			),
		)
	}

	return ctx
}
