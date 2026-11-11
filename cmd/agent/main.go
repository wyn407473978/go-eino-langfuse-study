package main

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
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

const defaultServicePrompt = `你是 {{service_name}} 的办事助手。
查询办理进度时使用 query_progress。
查询活动时使用 query_activities。
缺少编号或活动关键字时先询问用户。
未找到时如实说明，普通闲聊直接回答，不编造查询结果。`

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
	client, err := langfuseclient.NewClient(os.Getenv("LANGFUSE_BASE_URL"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"))
	if err != nil {
		fmt.Printf("创建langfuse client 失败: %v", err)
		return
	}

	promptCache, err := langfuseclient.NewPromptCache(client, 3*time.Second, 30*time.Second)
	if err != nil {
		fmt.Printf("获取缓存提示词失败:%v", err)
		return
	}

	promptResult, compiledinstruction, err := initPrompt(ctx, promptCache)
	if err != nil {
		log.Printf("准备 Prompt 失败: %v", err)
		return
	}
	log.Printf(
		"本次 Prompt：来源=%s，名称=%s，版本=%d",
		promptResult.Source,
		promptResult.Prompt.Name,
		promptResult.Prompt.Version,
	)

	if promptResult.RefreshError != nil {
		log.Printf("Prompt 获取降级：%v", promptResult.RefreshError)
	}
	// 所有来源都保留基础追踪。
	var selectedHandler callbacks.Handler = langfuseHandler

	// 远端、新鲜缓存、旧缓存都有真实远程版本。
	if promptResult.Source != "fallback" {
		promptHandler, err := observability.NewPromptHandler(
			langfuseHandler,
			promptResult.Prompt.Name,
			promptResult.Prompt.Version,
		)
		if err != nil {
			log.Printf("Prompt 追踪配置失败：%v", err)
			return
		}

		selectedHandler = promptHandler
	}

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
	}, adk.WithCallbacks(selectedHandler))

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

func initPrompt(parentCtx context.Context, cache *langfuseclient.PromptCache) (
	*langfuseclient.PromptResult,
	string,
	error,
) {
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()

	result, err := cache.GetTextPromptWithFallback(ctx, "service-assistant", "production", defaultServicePrompt)
	if err != nil {
		return nil, "", fmt.Errorf("提示词获取失败：%w", err)
	}
	standardPrompt, err := langfuseclient.CompileText(result.Prompt.Prompt, map[string]string{
		"service_name": "政务服务中心",
	})
	if err != nil {
		return result, "", fmt.Errorf("提示词编译失败：%w", err)
	}
	if strings.TrimSpace(standardPrompt) == "" {
		return nil, "", fmt.Errorf("提示词不能为空")
	}
	fmt.Println(standardPrompt)

	return result, standardPrompt, nil

}
