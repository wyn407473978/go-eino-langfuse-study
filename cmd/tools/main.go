package main

import (
	"context"
	"fmt"
	tools2 "go-langfuse-demo/internal/tools"
	"log"
)

func main() {

	progressTool, err := tools2.NewProgressTool()
	if err != nil {
		log.Fatal(err)
		return
	}
	result, err := progressTool.InvokableRun(
		context.Background(),
		`{"business_id":"CASE-001"}`,
	)
	if err != nil {
		log.Fatal(err)
		return
	}
	fmt.Println(result)
}
