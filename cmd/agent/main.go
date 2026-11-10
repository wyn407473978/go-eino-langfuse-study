package main

import (
	"context"
	"fmt"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"go-langfuse-demo/internal/tools"
	"log"
	"os"
	"time"
)

func main() {
	fmt.Println("agent")
	progressTool, err := tools.NewProgressTool()
	if err != nil {
		log.Fatal(err)
		return
	}
	activitiesTool, err := tools.NewActivitiesTool()
	if err != nil {
		log.Fatal(err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   "deepseek-flash",
		APIKey:  os.Getenv("DS_API_KEY"),
		BaseURL: os.Getenv("DS_BASE_URL"),
		Timeout: 60 * time.Second,
	})
	if err != nil {
		log.Println("模型创建失败")
		log.Fatal(err)
		return
	}

	//- 办事进度使用 query_progress。
	//- 活动查询使用 query_activities。
	//- 缺少必要参数时询问用户。
	//- 未找到时如实说明，闲聊直接回答。
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "service_assistant",
		Description: "提供办事进度查询的助手",
		Instruction: "你是办事助手。查询办理进度时使用 query_progress；" +
			"活动查询使用 query_activities。" +
			"缺少编号时先询问用户；未找到时如实说明；" +
			"普通闲聊直接回答，不编造办理状态。",
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

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: false,
	})

	iter := runner.Run(ctx, []*schema.Message{
		schema.UserMessage("有没有 Go 相关活动？"),
	})

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

		fmt.Printf("角色：%s，工具：%s\n", msg.Role, output.ToolName)
		fmt.Printf("正文：%s\n工具调用：%+v\n", msg.Content, msg.ToolCalls)
	}

}
