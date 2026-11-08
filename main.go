package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go-langfuse-demo/model/req"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
)

const chatTimeout = 60 * time.Second

func main() {
	engine := gin.Default()
	ctx := context.Background()
	apiKey := os.Getenv("DS_API_KEY")
	if apiKey == "" {
		fmt.Println("DS_API_KEY env var not set")
		return
	}
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   "deepseek-flash",
		BaseURL: "https://api.deepseek.com",
		APIKey:  apiKey,
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	engine.POST("/chat", newChatHandler(func(ctx context.Context, message string) (string, error) {
		return ModelChat(ctx, chatModel, message)
	}, chatTimeout))

	if err := engine.Run(); err != nil {
		fmt.Println(err)
	}
}

// 传入聊天函数和超时时间，便于独立验证 HTTP 行为。
func newChatHandler(chat func(context.Context, string) (string, error), timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		var chatReq req.ModelChatReq
		if err := c.ShouldBindJSON(&chatReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误或缺少必填字段"})
			return
		}
		chatReq.Message = strings.TrimSpace(chatReq.Message)
		if chatReq.Message == "" || strings.TrimSpace(chatReq.UserID) == "" || strings.TrimSpace(chatReq.SessionID) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "message、user_id 和 session_id 不能为空白"})
			return
		}

		requestCtx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()

		answer, err := chat(requestCtx, chatReq.Message)
		if err != nil {
			log.Printf("模型调用失败: %v", err)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "模型调用超时"})
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"error": "模型调用失败"})
			}
			return
		}
		c.JSON(http.StatusOK, req.ModelChatResp{Answer: answer})
	}
}

func ModelChat(ctx context.Context, chatModel *openai.ChatModel, userInput string) (string, error) {
	if chatModel == nil {
		return "", errors.New("chatModel is nil")
	}
	if strings.TrimSpace(userInput) == "" {
		return "", errors.New("userInput is empty")
	}
	messages := []*schema.Message{
		schema.SystemMessage("你是一个AI助手"),
		schema.UserMessage(userInput),
	}
	msg, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("模型调用失败: %w", err)
	}
	return msg.Content, nil
}
