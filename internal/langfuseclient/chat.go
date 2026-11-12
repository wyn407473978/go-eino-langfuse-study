package langfuseclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type ChatPromptItem struct {
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChatPrompt struct {
	Name    string           `json:"name"`
	Version int              `json:"version"`
	Type    string           `json:"type"`
	Prompt  []ChatPromptItem `json:"prompt"`
	Labels  []string         `json:"labels"`
}

// 调用LangFuse 地址真实的获取提示词数据
func (c *Client) GetChatPrompt(ctx context.Context, name, label string) (*ChatPrompt, error) {
	chatPrompt := new(ChatPrompt)

	if strings.TrimSpace(label) == "" {
		log.Printf("label is empty %v", label)
		return nil, errors.New("label is empty")
	}
	if strings.TrimSpace(name) == "" {
		log.Printf("name is empty %v", name)
		return nil, errors.New("name is empty")
	}

	endpoint := strings.TrimRight(c.baseURL, "/") +
		"/api/public/v2/prompts/" + url.PathEscape(name)

	query := url.Values{}
	query.Set("label", label)

	address := endpoint + "?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		log.Printf("reqesut create faile：%v", err.Error())
		return nil, err
	}
	request.SetBasicAuth(c.publicKey, c.secretKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("获取 Prompt 请求失败: %w", err)
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			log.Printf("close response body error:%v", err.Error())
			return
		}
	}()
	responseCode := response.StatusCode
	if responseCode != http.StatusOK {
		return nil, &HTTPStatusError{
			StatusCode: responseCode,
		}
	}
	if err := json.NewDecoder(response.Body).Decode(chatPrompt); err != nil {
		return nil, fmt.Errorf("解码 Prompt 响应失败: %w", err)
	}

	if chatPrompt.Type != "chat" {
		return nil, fmt.Errorf(
			"期望 chat Prompt，实际类型：%q",
			chatPrompt.Type,
		)
	}
	if len(chatPrompt.Prompt) == 0 {
		return nil, fmt.Errorf("chat Prompt 消息列表不能为空")
	}

	return chatPrompt, nil

}
