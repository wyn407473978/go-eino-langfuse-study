package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go-langfuse-demo/internal/langfuseclient"
	"go-langfuse-demo/internal/observability"
	"go-langfuse-demo/internal/tools"
	"go.opentelemetry.io/otel/trace"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	client, err := langfuseclient.NewClient(os.Getenv("LANGFUSE_BASE_URL"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	callbackHandler, err := langfuse.NewHandler(ctx, &langfuse.Config{
		Host:                       os.Getenv("LANGFUSE_BASE_URL"),
		PublicKey:                  os.Getenv("LANGFUSE_PUBLIC_KEY"),
		SecretKey:                  os.Getenv("LANGFUSE_SECRET_KEY"),
		Environment:                "development",
		ServiceName:                "go-langfuse-agent",
		CollapseAgentInternalSpans: true,
	})
	if err != nil {
		log.Printf("langfuse 回调创建失败：%v", err)
		return
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer shutdownCancel()
		err := callbackHandler.Shutdown(shutdownCtx)
		if err != nil {
			log.Printf("langfsue handler shutdown err:%v", err)
			return
		}
	}()
	progressTool, err := tools.NewProgressTool()
	if err != nil {
		log.Printf("进度工具创建失败：%v", err)
		return
	}

	activitiesTool, err := tools.NewActivitiesTool()
	if err != nil {
		log.Printf("活动工具创建失败：%v", err)
		return
	}

	toolList := []tool.BaseTool{
		progressTool,
		activitiesTool,
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   "deepseek-flash",
		BaseURL: "https://api.deepseek.com",
		APIKey:  os.Getenv("DS_API_KEY"),
		Timeout: 60 * time.Second,
	})
	if err != nil {
		log.Printf("模型创建失败: %v", err)
		return
	}
	history := make([]langfuseclient.ChatMessage, 0)
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("请输入问题，输入 exit 退出。")
	sessionID := fmt.Sprintf("chat-%d", time.Now().UnixNano())
	for {
		fmt.Print("\n你：")
		if !scanner.Scan() {
			break
		}

		question := strings.TrimSpace(scanner.Text())
		if question == "" {
			continue
		}
		if strings.EqualFold(question, "exit") {
			break
		}

		answer, err := runChatOnce(
			ctx, client, chatModel, question, callbackHandler, sessionID, history, toolList,
		)
		if err != nil {
			log.Printf("本次问答失败：%v", err)
			continue
		}

		fmt.Println("助手：", answer)

		// 整轮成功后，才保存这两个消息。
		history = append(history,
			langfuseclient.ChatMessage{
				Role: "user", Content: question,
			},
			langfuseclient.ChatMessage{
				Role: "assistant", Content: answer,
			},
		)

		// 保留最近10轮，每轮两条消息。
		if len(history) > 20 {
			history = history[len(history)-20:]
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("读取输入失败：%v", err)
	}

}

func runChatOnce(
	parentCtx context.Context,
	client *langfuseclient.Client,
	chatModel *openai.ChatModel,
	question string,
	handler *langfuse.CallbackHandler,
	sessionID string,
	history []langfuseclient.ChatMessage,
	tools []tool.BaseTool,
) (string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 60*time.Second)
	defer cancel()

	traceCtx := handler.StartTrace(ctx,
		langfuse.WithName("chat-prompt-demo"),
		langfuse.WithInput(question),
		langfuse.WithUserID("demo-user"),
		langfuse.WithSessionID(sessionID),
		langfuse.WithTags("chat", "lesson-d19"),
		langfuse.WithMetadata(map[string]string{
			"prompt_source":    "remote",
			"history_messages": fmt.Sprint(len(history)),
		}),
	)
	traceOutput := "Agent 未完成"
	defer func() {
		handler.EndTrace(traceCtx, traceOutput)
	}()

	log.Printf(
		"trace_id=%s",
		trace.SpanFromContext(traceCtx).SpanContext().TraceID().String(),
	)

	promptCtx, promptCancel := context.WithTimeout(ctx, 10*time.Second)

	defer promptCancel()
	prompt, err := client.GetChatPrompt(
		promptCtx,
		"service-assistant-chat",
		"production",
	)
	if err != nil {
		log.Printf("获取 chat Prompt 失败：%v", err)
		return "", err
	}
	promptHandler, err := observability.NewPromptHandler(
		handler,
		prompt.Name,
		prompt.Version,
	)
	if err != nil {
		return "", fmt.Errorf("Prompt 追踪配置失败：%w", err)
	}

	fmt.Printf(
		"名称=%s，版本=%d，类型=%s\n",
		prompt.Name, prompt.Version, prompt.Type,
	)

	for i, item := range prompt.Prompt {
		if item.Type == "placeholder" {
			fmt.Printf("第%d项：历史占位=%s\n", i+1, item.Name)
			continue
		}

		fmt.Printf(
			"第%d项：角色=%s，正文=%q\n",
			i+1, item.Role, item.Content,
		)
	}

	messages, err := langfuseclient.CompileChat(
		prompt.Prompt,
		map[string]string{
			"service_name": "政务服务中心",
			"question":     question,
		},
		map[string][]langfuseclient.ChatMessage{
			"chat_history": history,
		},
	)
	if err != nil {
		log.Printf("编译 chat Prompt 失败：%v", err)
		return "", err
	}

	for i, message := range messages {
		fmt.Printf(
			"第%d条：角色=%s，正文=%q\n",
			i+1, message.Role, message.Content,
		)
	}

	chatModelAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "对话",
		Model:         chatModel,
		Instruction:   "",
		MaxIterations: 5,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools,
			},
		}})

	if err != nil {
		log.Printf("chatModelAgent 创建失败%v", err)
		return "", err
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           chatModelAgent,
		EnableStreaming: false,
	})
	chatMessages := make([]*schema.Message, 0)
	for _, message := range messages {
		chatMessages = append(chatMessages, &schema.Message{
			Role:    schema.RoleType(message.Role),
			Content: message.Content,
		})
	}
	run := runner.Run(traceCtx, chatMessages, adk.WithCallbacks(promptHandler))
	finalAnswer := ""
	for {
		event, flag := run.Next()
		if !flag {
			break
		}
		if event.Err != nil {
			return "", fmt.Errorf("Agent 执行失败：%w", event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		message, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			fmt.Printf("获取消息出错了：%v\n", err.Error())
			return "", err
		}
		if message == nil {
			continue
		}

		if message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			finalAnswer = message.Content
		}
	}
	if err := ctx.Err(); err != nil {
		log.Printf("请求未完成：%v", err)
		return "", err
	}
	if finalAnswer == "" {
		return "", fmt.Errorf("Agent 未返回最终回答")
	}

	fmt.Println("Agent 回答：", finalAnswer)
	traceOutput = finalAnswer
	return finalAnswer, nil
}
