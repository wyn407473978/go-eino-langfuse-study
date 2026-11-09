package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"log"
	"os"
	"time"
)

func main() {

	waitExport := flag.Bool("wait-export", true, "退出前是否等待追踪导出")
	flag.Parse()

	langfuseConfig := langfuse.Config{
		Host:               os.Getenv("LANGFUSE_BASE_URL"),
		PublicKey:          os.Getenv("LANGFUSE_PUBLIC_KEY"),
		SecretKey:          os.Getenv("LANGFUSE_SECRET_KEY"),
		ServiceName:        "demo",
		BatchTimeout:       time.Second * 30,
		MaxExportBatchSize: 128,
	}

	ctx := context.Background()
	langfuseHandler, err := langfuse.NewHandler(ctx, &langfuseConfig)
	if err != nil {
		log.Fatal(err)
		return
	}

	startTraceContext := langfuseHandler.StartTrace(ctx, langfuse.WithName("export-lab"), langfuse.WithInput(fmt.Sprintf("wait-export=%t", *waitExport)))

	langfuseHandler.EndTrace(startTraceContext, "测试结束")

	if *waitExport {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := langfuseHandler.Shutdown(shutdownCtx); err != nil {
			log.Printf("Langfuse 导出关闭失败: %v", err)
			return
		}
		log.Println("Langfuse 导出关闭完成")
	}

}
