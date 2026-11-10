package main

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go-langfuse-demo/internal/langfuseclient"
	"go-langfuse-demo/internal/tools"
	"go.opentelemetry.io/otel/trace"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	langfuseConfig := langfuse.Config{
		Host:                       os.Getenv("LANGFUSE_BASE_URL"),
		PublicKey:                  os.Getenv("LANGFUSE_PUBLIC_KEY"),
		SecretKey:                  os.Getenv("LANGFUSE_SECRET_KEY"),
		Environment:                "development",
		ServiceName:                "go-langfuse-agent",
		CollapseAgentInternalSpans: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	langfuseHandler, err := langfuse.NewHandler(ctx, &langfuseConfig)
	if err != nil {
		log.Printf("Langfuse 初始化失败: %v", err)
		return
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		defer shutdownCancel()

		if err := langfuseHandler.Shutdown(shutdownCtx); err != nil {
			log.Printf("Langfuse 关闭失败: %v", err)
		}
	}()
	fmt.Println("agent")
	progressTool, err := tools.NewProgressTool()
	if err != nil {
		log.Printf("progressTool初始化失败：%v", err)
		return
	}
	activitiesTool, err := tools.NewActivitiesTool()
	if err != nil {
		log.Printf("活动工具初始化失败：%v", err)
		return
	}

	managedPrompt, compiledinstruction, err := initPrompt(ctx)
	if err != nil {
		log.Printf("准备 Prompt 失败: %v", err)
		return
	}

	log.Printf("本次使用 Prompt：%s，版本：%d",
		managedPrompt.Name, managedPrompt.Version)

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   "deepseek-flash",
		APIKey:  os.Getenv("DS_API_KEY"),
		BaseURL: os.Getenv("DS_BASE_URL"),
		Timeout: 60 * time.Second,
	})
	if err != nil {
		log.Printf("模型创建失败:%v", err)
		return
	}

	//- 办事进度使用 query_progress。
	//- 活动查询使用 query_activities。
	//- 缺少必要参数时询问用户。
	//- 未找到时如实说明，闲聊直接回答。
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "service_assistant",
		Description:   "提供办事进度查询的助手",
		Instruction:   compiledinstruction,
		Model:         chatModel,
		MaxIterations: 5,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{progressTool, activitiesTool},
			},
		},
	})
	if err != nil {
		log.Printf("Agent 创建失败: %v", err)
		return
	}

	question := "有没有 Go 相关活动？"

	traceCtx := langfuseHandler.StartTrace(ctx,
		langfuse.WithName("agent-demo"),
		langfuse.WithInput(question),
		langfuse.WithTags("agent", "lesson-d15"),
	)

	traceOutput := "Agent 未完成"
	defer func() {
		langfuseHandler.EndTrace(traceCtx, traceOutput)
	}()
	log.Printf("trace_id=%s",
		trace.SpanFromContext(traceCtx).SpanContext().TraceID().String())

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: false,
	})

	iter := runner.Run(traceCtx, []*schema.Message{
		schema.UserMessage(question),
	}, adk.WithCallbacks(langfuseHandler))

	finalAnswer := ""
	for {
		event, ok := iter.Next()
		if !ok {
			break // 正常结束，不代表读取失败
		}
		if event.Err != nil {
			log.Printf("Agent 执行失败: %v", event.Err)
			return
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		output := event.Output.MessageOutput
		msg, err := output.GetMessage()
		if err != nil {
			log.Printf("读取消息失败: %v", err)
			return
		}
		if msg == nil {
			continue
		}
		if msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
			finalAnswer = msg.Content
		}

		fmt.Printf("角色：%s，工具：%s\n", msg.Role, output.ToolName)
		fmt.Printf("正文：%s\n工具调用：%+v\n", msg.Content, msg.ToolCalls)
	}
	if err := ctx.Err(); err != nil {
		log.Printf("Agent 请求未完成: %v", err)
		return
	}
	if finalAnswer == "" {
		log.Println("Agent 未返回最终正文")
		return
	}

	traceOutput = finalAnswer

}

func initPrompt(parentCtx context.Context) (
	*langfuseclient.TextPrompt,
	string,
	error,
) {
	client, err := langfuseclient.NewClient(os.Getenv("LANGFUSE_BASE_URL"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"))
	if err != nil {
		return nil, "", fmt.Errorf("langfuse客户端初始化失败: %w", err)
	}
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	prompt, err := client.GetTextPrompt(
		ctx, "service-assistant", "production",
	)
	if err != nil {
		return nil, "", fmt.Errorf("提示词获取失败：%w", err)
	}
	standardPrompt, err := langfuseclient.CompileText(prompt.Prompt, map[string]string{
		"service_name": "政务服务中心",
	})
	if err != nil {
		return prompt, "", fmt.Errorf("提示词编译失败：%w", err)
	}
	if strings.TrimSpace(standardPrompt) == "" {
		return prompt, "", fmt.Errorf("提示词不能为空,%w", err)
	}
	fmt.Println(standardPrompt)

	return prompt, standardPrompt, nil

}
