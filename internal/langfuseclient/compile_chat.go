package langfuseclient

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompileChat 编译普通消息中的文本变量，并按模板顺序展开消息占位项。
// 采用本练习的严格策略：缺少或多余的参数、未知类型/角色均返回错误。
// 历史正文按字面插入，不再替换变量；空历史允许，输入保持不变。
func CompileChat(
	template []ChatPromptItem,
	variables map[string]string,
	placeholders map[string][]ChatMessage,
) ([]ChatMessage, error) {
	messages := make([]ChatMessage, 0)
	usedVariables := make(map[string]bool)
	usedPlaceholders := make(map[string]bool)
	pattern := regexp.MustCompile(
		`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`,
	)

	for i, item := range template {
		switch item.Type {
		case "placeholder":
			if strings.TrimSpace(item.Name) == "" {
				return nil, fmt.Errorf("第%d项的消息占位名称不能为空", i+1)
			}
			// 用键是否存在区分“没有传历史”和“明确传入空历史”。
			history, ok := placeholders[item.Name]
			if !ok {
				return nil, fmt.Errorf("缺少消息占位参数：%s", item.Name)
			}
			for j, message := range history {
				if !validChatRole(message.Role) {
					return nil, fmt.Errorf("消息占位%s的第%d条历史角色不支持：%q", item.Name, j+1, message.Role)
				}
			}
			usedPlaceholders[item.Name] = true
			// 追加结构体值，返回切片不复用调用方的历史切片。
			// 不编译历史正文，其中的 {{...}} 必须保持原样。
			messages = append(messages, history...)

		case "", "message":
			if !validChatRole(item.Role) {
				return nil, fmt.Errorf("第%d项的消息角色不支持：%q", i+1, item.Role)
			}
			// CompileText 会拒绝额外变量，因此每条消息只传自己的变量子集。
			localVariables := make(map[string]string)
			for _, match := range pattern.FindAllStringSubmatch(item.Content, -1) {
				name := match[1]
				value, ok := variables[name]
				if !ok {
					return nil, fmt.Errorf("第%d项缺少模板变量：%s", i+1, name)
				}
				localVariables[name] = value
				usedVariables[name] = true
			}
			content, err := CompileText(item.Content, localVariables)
			if err != nil {
				return nil, fmt.Errorf("编译第%d项消息失败：%w", i+1, err)
			}
			messages = append(messages, ChatMessage{
				Role:    item.Role,
				Content: content,
			})

		default:
			return nil, fmt.Errorf("第%d项的消息类型不支持：%q", i+1, item.Type)
		}
	}

	// 从整个模板判断多余参数，而不是从单条消息判断；排序使错误提示稳定。
	var extraVariables []string
	for name := range variables {
		if !usedVariables[name] {
			extraVariables = append(extraVariables, name)
		}
	}
	if len(extraVariables) > 0 {
		sort.Strings(extraVariables)
		return nil, fmt.Errorf("存在未使用的模板变量：%s", strings.Join(extraVariables, ", "))
	}
	var extraPlaceholders []string
	for name := range placeholders {
		if !usedPlaceholders[name] {
			extraPlaceholders = append(extraPlaceholders, name)
		}
	}
	if len(extraPlaceholders) > 0 {
		sort.Strings(extraPlaceholders)
		return nil, fmt.Errorf("存在未使用的消息占位参数：%s", strings.Join(extraPlaceholders, ", "))
	}

	return messages, nil
}

func validChatRole(role string) bool {
	switch role {
	case "system", "user", "assistant":
		return true
	default:
		return false
	}
}
