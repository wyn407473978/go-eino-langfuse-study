package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
	"io"
	"os"
	"time"
)

func main() {
	fmt.Println("Main")

	ctx := context.Background()
	timeout, cancelFunc := context.WithTimeout(ctx, 60*time.Second)
	defer cancelFunc()
	chatModel, err := openai.NewChatModel(timeout, &openai.ChatModelConfig{
		APIKey:  os.Getenv("DS_API_KEY"),
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-flash",
	})
	if err != nil {
		fmt.Println("模型连接失败")
		return
	}
	messages := []*schema.Message{schema.SystemMessage("你是一个专业的GO 开发工程师"), schema.UserMessage("用三个小段解释 Go 的 interface，每段举一个例子")}
	stream, err := chatModel.Stream(timeout, messages)
	if err != nil {
		fmt.Println("模型调用失败")
		return
	}
	defer stream.Close()

	var schemas []*schema.Message

	for {
		recv, err := stream.Recv()

		if errors.Is(err, io.EOF) {
			fmt.Println("流式读取结束")
			break
		}
		if err != nil {
			fmt.Printf("出错了%v\n", err)
			return
		}

		fmt.Print(recv.Content)

		schemas = append(schemas, recv)
	}
	fmt.Println()

	fullMessages, err := schema.ConcatMessages(schemas)
	if err != nil {
		fmt.Println("消息聚合失败")
		return
	}

	fmt.Println(fullMessages.Content)
	if fullMessages.ResponseMeta == nil ||
		fullMessages.ResponseMeta.Usage == nil {
		fmt.Println("本次未返回 usage")
	} else {
		usage := fullMessages.ResponseMeta.Usage
		fmt.Printf(
			"输入 token：%d，输出 token：%d，总 token：%d\n",
			usage.PromptTokens,
			usage.CompletionTokens,
			usage.TotalTokens,
		)
	}

}
