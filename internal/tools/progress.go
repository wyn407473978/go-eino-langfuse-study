package tools

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"strings"
)

type ProgressRequest struct {
	BusinessID string `json:"business_id" jsonschema:"required,description=办事编号，例如 CASE-001"`
}

type ProgressResponse struct {
	Found      bool   `json:"found"`
	BusinessID string `json:"business_id"`
	Status     string `json:"status,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func QueryProgress(ctx context.Context, input *ProgressRequest) (*ProgressResponse, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if input == nil {
		return nil, errors.New("参数错误")

	}
	businessID := strings.TrimSpace(input.BusinessID)
	if businessID == "" {
		return nil, errors.New("参数为空")
	}
	data := []struct {
		ID, Status, Detail string
	}{
		{ID: "CASE-001",
			Status: "reviewing",
			Detail: "材料审核中"},
		{ID: "CASE-002",
			Status: "completed",
			Detail: "办理完成",
		},
	}
	for _, d := range data {
		if d.ID == businessID {
			return &ProgressResponse{
				Found:      true,
				BusinessID: d.ID,
				Status:     d.Status,
				Detail:     d.Detail,
			}, nil
		}
	}
	return &ProgressResponse{
		Found:      false,
		BusinessID: businessID,
	}, nil
}

func NewProgressTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_progress",
		"根据办事编号查询办理状态；未找到时返回 found=false",
		QueryProgress,
	)

}
