package main

import (
	"context"
	"go-langfuse-demo/internal/langfuseclient"
	"log"
	"os"
	"time"
)

const defaultServicePrompt = `你是 {{service_name}} 的办事助手。
查询办理进度时使用 query_progress。
查询活动时使用 query_activities。
缺少编号或活动关键字时先询问用户。
未找到时如实说明，普通闲聊直接回答，不编造查询结果。`

func main() {

	client, err := langfuseclient.NewClient(os.Getenv("LANGFUSE_BASE_URL"), os.Getenv("LANGFUSE_PUBLIC_KEY"), os.Getenv("LANGFUSE_SECRET_KEY"))
	if err != nil {
		log.Printf("Error creating client: %v", err)
		return
	}
	cache, err := langfuseclient.NewPromptCache(client, 3*time.Second, 30*time.Second)
	if err != nil {
		log.Printf("Error creating cache: %v", err)
		return
	}

	ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelFunc()
	for i := 1; i <= 3; i++ {
		if i == 3 {
			time.Sleep(4 * time.Second)
		}

		startedAt := time.Now()
		result, err := cache.GetTextPromptWithFallback(
			ctx,
			"service-assistant",
			"production",
			defaultServicePrompt,
		)
		if err != nil {
			log.Printf("获取失败: %v", err)
			return
		}
		if result.RefreshError != nil {
			log.Printf(
				"远端获取失败，使用来源=%s，原因=%v",
				result.Source,
				result.RefreshError,
			)
		}

		log.Printf("第%d次：来源=%s，名称=%s，版本=%d，耗时=%s",
			i, result.Source, result.Prompt.Name,
			result.Prompt.Version, time.Since(startedAt))
	}

}
