package tools

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"strings"
)

type ActivityRequest struct {
	Keyword string `json:"keyword" jsonschema:"required,description=活动名称关键字，例如 Go"`
}

type Activity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

type ActivityResponse struct {
	Found      bool       `json:"found"`
	Keyword    string     `json:"keyword"`
	Activities []Activity `json:"activities"`
}

//  2. |  |  |  |
//     |---|---|---|
//     | ACT-001 | Go 入门分享会 | 线上 |
//     | ACT-002 | AI 应用实践沙龙 | 科技馆 |
func QueryActivities(ctx context.Context, input *ActivityRequest) (*ActivityResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	activities := []Activity{
		{"ACT-001", "Go 入门分享会", "线上"},
		{"ACT-002", "AI 应用实践沙龙", "科技馆"},
	}
	if input == nil {
		return nil, errors.New("参数错误")
	}
	userInput := strings.ToLower(strings.TrimSpace(input.Keyword))
	if userInput == "" {
		return nil, errors.New("参数不能为空")
	}

	var result = new(ActivityResponse)
	result.Keyword = userInput
	matches := make([]Activity, 0)
	for _, a := range activities {
		if strings.Contains(strings.ToLower(a.Name), userInput) {
			matches = append(matches, a)
		}
	}
	result.Found = len(matches) > 0
	result.Activities = matches
	return result, nil

}

func NewActivitiesTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"query_activities",
		"根据活动名称关键字查询模拟活动，未找到时返回 found=false",
		QueryActivities,
	)
}
