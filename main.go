package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
	"go-langfuse-demo/internal/memory"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go-langfuse-demo/model/req"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/gin-gonic/gin"
)

const chatTimeout = 60 * time.Second

func main() {
	engine := gin.Default()

	engine.GET("/sse", func(c *gin.Context) {
		c.Header("Content-type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		datas := []string{"第一块", "第二块", "第三块"}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for _, value := range datas {
			select {
			case <-ticker.C:
				c.SSEvent("chunk", gin.H{"content": value})
				c.Writer.Flush()
			case <-c.Request.Context().Done():
				log.Println("SSE 客户端已断开，停止发送")

				return

			}
		}
		c.SSEvent("done", gin.H{"finished": true})
		c.Writer.Flush()

	})

	server := http.Server{
		Addr:    ":8080",
		Handler: engine,
	}

	serverErrors := make(chan error, 1)

	notifyContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx := notifyContext
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
		panic(err)
	}

	//LangFuse
	config := langfuse.Config{
		Host:        os.Getenv("LANGFUSE_BASE_URL"),
		PublicKey:   os.Getenv("LANGFUSE_PUBLIC_KEY"),
		SecretKey:   os.Getenv("LANGFUSE_SECRET_KEY"),
		ServiceName: "go-langfuse-demo",
		Environment: "development",
	}
	handler, err := langfuse.NewHandler(ctx, &config)
	if err != nil {
		panic(err)
	}
	defer func() {
		shoutDownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err = handler.Shutdown(shoutDownCtx)
		if err != nil {
			log.Printf("Langfuse 关闭失败: %v", err)
		}
	}()
	callbacks.AppendGlobalHandlers(handler)
	store := memory.NewStore()
	engine.POST("/chat/stream", newStreamChatHandler(chatModel, handler, store, chatTimeout))

	engine.POST("/chat", newChatHandler(func(ctx context.Context, chatReq req.ModelChatReq) (string, error) {
		traceSessionID := url.QueryEscape(chatReq.UserID) + ":" + url.QueryEscape(chatReq.SessionID)
		traceContex := handler.StartTrace(ctx,
			langfuse.WithName("chat-response"),
			langfuse.WithInput(chatReq.Message),
			langfuse.WithUserID(chatReq.UserID),
			langfuse.WithSessionID(traceSessionID), langfuse.WithTags("chat", "memory"), langfuse.WithMetadata(map[string]string{"feature": "chat", "channel": "api"}), langfuse.WithRelease("lesson-d08"),
		)
		chat, err := ModelChat(traceContex, chatModel, chatReq, store)
		if err != nil {
			handler.EndTrace(traceContex, "模型调用失败")
			return "模型调用失败", err
		}
		defer handler.EndTrace(traceContex, chat)
		return chat, err
	}, chatTimeout))
	go func() {
		err := server.ListenAndServe()
		serverErrors <- err
	}()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP 服务运行失败：%v", err)
		}

	case <-notifyContext.Done():
		log.Println("收到退出信号开始关闭服务")

	}
	timeout, cancelFunc := context.WithTimeout(context.Background(), time.Second*65)
	defer cancelFunc()

	if err := server.Shutdown(timeout); err != nil {
		log.Printf("HTTP 优雅关闭失败:%v", err)
	}
}

// 传入聊天函数和超时时间，便于独立验证 HTTP 行为。
func newChatHandler(chat func(context.Context, req.ModelChatReq) (string, error), timeout time.Duration) gin.HandlerFunc {
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

		answer, err := chat(requestCtx, chatReq)
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

func newStreamChatHandler(chatModel *openai.ChatModel, langfuseHandler *langfuse.CallbackHandler, store *memory.Store, timeout time.Duration) gin.HandlerFunc {
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
		ctx := c.Request.Context()
		timeoutCtx, cancelFunc := context.WithTimeout(ctx, timeout)
		defer cancelFunc()
		traceSessionID := url.QueryEscape(chatReq.UserID) + ":" + url.QueryEscape(chatReq.SessionID)
		streamTraceContext := langfuseHandler.StartTrace(timeoutCtx, langfuse.WithName("chat-stream"),
			langfuse.WithInput(chatReq.Message),
			langfuse.WithUserID(chatReq.UserID),
			langfuse.WithSessionID(traceSessionID),
			langfuse.WithTags("chat", "memory", "stream"),
			langfuse.WithMetadata(map[string]string{
				"feature": "chat",
				"channel": "sse",
			}),
			langfuse.WithRelease("lesson-d12"))
		traceOutput := "请求未完成"

		defer func() {
			langfuseHandler.EndTrace(streamTraceContext, traceOutput)
		}()
		traceID := trace.SpanFromContext(streamTraceContext).SpanContext().TraceID().String()
		session, err := store.LockSession(streamTraceContext, chatReq.UserID, chatReq.SessionID)
		if err != nil {
			log.Printf("获取会话锁失败: %v", err)

			if c.Request.Context().Err() != nil {
				return
			}

			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "等待会话锁超时"})
			return
		}
		defer session()
		var messages = []*schema.Message{}

		messages = append(messages, schema.SystemMessage("你是一个AI助手"))
		if store != nil {
			load := store.Load(chatReq.UserID, chatReq.SessionID)
			messages = append(messages, load...)
		}
		userMessage := schema.UserMessage(chatReq.Message)
		messages = append(messages, userMessage)
		stream, err := chatModel.Stream(streamTraceContext, messages)
		if err != nil {
			log.Printf("模型流创建失败: %v", err)
			if errors.Is(err, context.DeadlineExceeded) ||
				errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": "模型调用超时"})
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"error": "模型调用失败"})
			}
			return
		}
		defer stream.Close()

		// 在这里实现
		c.Header("Content-type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Trace-ID", traceID)
		log.Printf("流式请求 trace_id=%s", traceID)

		c.Writer.Flush()

		var fullMessages []*schema.Message
		c.SSEvent("meta", gin.H{"trace_id": traceID})
		c.Writer.Flush()

		for {
			recv, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				fmt.Println("stream 响应结束")
				break
			}
			if err != nil {
				log.Printf("模型流读取结束: %v", err)

				if c.Request.Context().Err() != nil {
					log.Println("客户端已断开，停止模型流读取")
					return
				}

				c.SSEvent("error", gin.H{"message": "模型流式读取失败"})
				c.Writer.Flush()
				return
			}
			c.SSEvent("chunk", gin.H{
				"content": recv.Content,
			})

			fullMessages = append(fullMessages, recv)
			c.Writer.Flush()
		}
		if c.Request.Context().Err() != nil {
			return
		}
		if timeoutCtx.Err() != nil {
			c.SSEvent("error", gin.H{"message": "模型流式请求已取消或超时"})
			c.Writer.Flush()
			return
		}
		concatMessages, err := schema.ConcatMessages(fullMessages)
		if err != nil {
			log.Println("消息聚合失败")
			c.SSEvent("error", gin.H{"message": "消息聚合失败"})
			c.Writer.Flush()
			return
		}
		store.AppendTurn(chatReq.UserID, chatReq.SessionID, userMessage, concatMessages)
		traceOutput = concatMessages.Content

		c.SSEvent("done", gin.H{"finished": true})
		c.Writer.Flush()
	}
}

func ModelChat(ctx context.Context, chatModel *openai.ChatModel, chatReq req.ModelChatReq, store *memory.Store) (string, error) {

	if chatModel == nil {
		return "", errors.New("chatModel is nil")
	}
	if strings.TrimSpace(chatReq.Message) == "" {
		return "", errors.New("userInput is empty")
	}
	if store == nil {
		return "", errors.New("store is nil")
	}
	userID := chatReq.UserID
	sessionID := chatReq.SessionID
	session, err := store.LockSession(ctx, userID, sessionID)
	if err != nil {
		return "", err
	}
	defer session()

	systemMessage := []*schema.Message{schema.SystemMessage("你是一个AI助手")}
	parentSpan := trace.SpanFromContext(ctx)
	tracer := parentSpan.TracerProvider().Tracer("go-langfuse-demo/memory")

	_, historySpan := tracer.Start(ctx, "load-chat-history")
	traceSessionID := url.QueryEscape(userID) + ":" + url.QueryEscape(sessionID)

	historySpan.SetAttributes(
		attribute.String("langfuse.user.id", userID),
		attribute.String("langfuse.session.id", traceSessionID),
		attribute.StringSlice("langfuse.trace.tags", []string{"chat", "memory"}),
		attribute.String("langfuse.trace.metadata.feature", "chat"),
		attribute.String("langfuse.trace.metadata.channel", "api"),
		attribute.String("langfuse.release", "lesson-d08"),
		attribute.String("langfuse.environment", "development"),
	)
	history := store.Load(userID, sessionID)
	marshal, err := json.Marshal(history)
	historySpan.SetAttributes(attribute.String("langfuse.observation.type", "span"))
	if err == nil {
		historySpan.SetAttributes(attribute.String("chat-history", string(marshal)), attribute.String("langfuse.observation.output", string(marshal)))
	} else {
		historySpan.RecordError(err)
		historySpan.SetStatus(codes.Error, "历史消息序列化失败")
		log.Printf("历史消息序列化失败: %v", err)
	}
	historySpan.End()

	messages := append(systemMessage, history...)
	newMessage := schema.UserMessage(chatReq.Message)
	messages = append(messages, newMessage)
	msg, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("模型调用失败: %w", err)
	}
	store.AppendTurn(userID, sessionID, newMessage, msg)
	return msg.Content, nil
}
