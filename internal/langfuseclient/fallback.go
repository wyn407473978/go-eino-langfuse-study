package langfuseclient

import (
	"context"
	"errors"
	"strings"
)

func (c *PromptCache) GetTextPromptWithFallback(
	ctx context.Context,
	name, label string,
	fallback string,
) (*PromptResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	if strings.TrimSpace(label) == "" || strings.TrimSpace(name) == "" {
		return nil, errors.New("label or name can not bo empty")
	}

	if strings.TrimSpace(fallback) == "" {
		return nil, errors.New("fallback prompt must not be empty")
	}

	textPrompt, err := c.GetTextPrompt(ctx, name, label)
	if err == nil {
		return textPrompt, err
	}
	// 调用方取消后，结束本次请求。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	promptError := isTemporaryPromptError(err)
	if !promptError {
		return nil, err
	}
	return &PromptResult{
		Prompt: &TextPrompt{
			Name:    name,
			Version: 0,
			Type:    "text",
			Prompt:  fallback,
		},
		Source:       "fallback",
		RefreshError: err,
	}, nil

}
