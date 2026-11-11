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
	"time"
)

type Client struct {
	baseURL    string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

//定义本次需要的响应字段：

type TextPrompt struct {
	Name    string   `json:"name"`
	Version int      `json:"version"`
	Type    string   `json:"type"`
	Prompt  string   `json:"prompt"`
	Labels  []string `json:"labels"`
}

//实现两个方法：

func NewClient(baseURL, publicKey, secretKey string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" ||
		strings.TrimSpace(publicKey) == "" ||
		strings.TrimSpace(secretKey) == "" {
		return nil, errors.New("Langfuse 地址和项目凭据不能为空")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 Langfuse 地址失败: %w", err)
	}
	if parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Langfuse 地址必须包含 http/https 和 host")
	}
	return &Client{
		baseURL:    baseURL,
		publicKey:  publicKey,
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}, nil

}

func (c *Client) GetTextPrompt(ctx context.Context, name, label string) (*TextPrompt, error) {
	textPrompt := new(TextPrompt)

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
	if err := json.NewDecoder(response.Body).Decode(textPrompt); err != nil {
		return nil, fmt.Errorf("解码 Prompt 响应失败: %w", err)
	}

	if textPrompt.Type != "text" {
		return nil, fmt.Errorf("不支持的 Prompt 类型: %q", textPrompt.Type)
	}
	return textPrompt, nil

}
