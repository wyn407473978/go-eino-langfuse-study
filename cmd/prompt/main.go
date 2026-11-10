package main

import (
	"context"
	"fmt"
	"go-langfuse-demo/internal/langfuseclient"
	"log"
	"os"
	"time"
)

func main() {
	client, err := langfuseclient.NewClient(os.Getenv("LANGFUSE_BASE_URL"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	prompt, err := client.GetTextPrompt(
		ctx, "service-assistant", "production",
	)
	if err != nil {
		log.Printf("获取 Prompt 失败: %v", err)
		return
	}

	fmt.Printf("名称：%s，版本：%d，类型：%s\n",
		prompt.Name, prompt.Version, prompt.Type)
	fmt.Println("正文：", prompt.Prompt)
}
