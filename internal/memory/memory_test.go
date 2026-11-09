package memory

import (
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// Go 使用 TestXxx 函数组织测试，不需要创建“测试类”。
// 同包测试可以直接调用 NewStore，无需导入当前包。
func TestStore_AppendTurn(t *testing.T) {
	// 1. 准备：每个测试使用自己的 Store，避免测试之间共享状态。
	s := NewStore()
	userID := "1"
	sessionID := "1"
	userMessage := schema.UserMessage("我叫小林")
	assistantMessage := schema.AssistantMessage("你好，小林", nil)

	// 2. 执行：保存一轮，再通过公开方法读取。
	s.AppendTurn(userID, sessionID, userMessage, assistantMessage)
	messages := s.Load(userID, sessionID)

	// 3. 断言：实际结果必须符合预期。
	// 长度不正确时，后面的下标访问可能越界，所以用 Fatalf 停止当前测试。
	if len(messages) != 2 {
		t.Fatalf("消息数量 = %d，期望 2", len(messages))
	}
	// Errorf 标记失败，但继续检查其他互不依赖的条件。
	if messages[0] == nil || messages[0].Role != schema.User || messages[0].Content != "我叫小林" {
		t.Errorf("第一条消息应为用户问题，实际 = %+v", messages[0])
	}
	if messages[1] == nil || messages[1].Role != schema.Assistant || messages[1].Content != "你好，小林" {
		t.Errorf("第二条消息应为助手回答，实际 = %+v", messages[1])
	}
}

func TestStore_LoadUnknownSession(t *testing.T) {
	s := NewStore()
	messages := s.Load("unknown-user", "unknown-session")
	// 空切片和 nil 都符合“没有历史”的约定，不限制具体实现。
	if len(messages) != 0 {
		t.Fatalf("不存在的会话返回了 %d 条消息，期望 0", len(messages))
	}
}

func TestStore_SessionIsolation(t *testing.T) {
	s := NewStore()
	// 这些用户、会话的组合必须彼此独立。
	sessions := []struct {
		userID    string
		sessionID string
		question  string
	}{
		{userID: "user-1", sessionID: "session-1", question: "问题一"},
		{userID: "user-1", sessionID: "session-2", question: "问题二"},
		{userID: "user-2", sessionID: "session-1", question: "问题三"},
	}
	for _, session := range sessions {
		s.AppendTurn(session.userID, session.sessionID,
			schema.UserMessage(session.question),
			schema.AssistantMessage("回答："+session.question, nil))
	}
	for _, session := range sessions {
		// t.Run 创建子测试，失败输出会带上具体的用户和会话名称。
		t.Run(session.userID+"/"+session.sessionID, func(t *testing.T) {
			messages := s.Load(session.userID, session.sessionID)
			if len(messages) != 2 {
				t.Fatalf("消息数量 = %d，期望 2", len(messages))
			}
			if messages[0].Content != session.question || messages[1].Content != "回答："+session.question {
				t.Fatalf("当前会话读到了其他会话的数据: %+v", messages)
			}
		})
	}
}

func TestStore_LoadReturnsSliceCopy(t *testing.T) {
	s := NewStore()
	s.AppendTurn("u", "s", schema.UserMessage("原问题"), schema.AssistantMessage("原回答", nil))
	loaded := s.Load("u", "s")
	if len(loaded) != 2 {
		t.Fatalf("消息数量 = %d，期望 2", len(loaded))
	}

	// 替换切片中的元素，检查调用方是否能改到 Store 的底层数组。
	// 这里不修改 loaded[0].Content：消息对象本身仍按只读约定使用。
	loaded[0] = schema.UserMessage("被外部替换的问题")
	loaded[1] = schema.AssistantMessage("被外部替换的回答", nil)
	again := s.Load("u", "s")
	if len(again) != 2 {
		t.Fatalf("再次读取的消息数量 = %d，期望 2", len(again))
	}
	if again[0].Content != "原问题" || again[1].Content != "原回答" {
		t.Fatal("修改 Load 返回的切片影响了 Store 内部历史")
	}
}

func TestStore_KeepsLatestTenTurns(t *testing.T) {
	// 同一套逻辑检查不足10轮、恰好10轮、超过10轮和持续截断。
	// 这种将输入列成表的写法叫表驱动测试。
	for _, turns := range []int{1, 9, 10, 11, 25} {
		t.Run(fmt.Sprintf("%d轮", turns), func(t *testing.T) {
			s := NewStore()
			for turn := 1; turn <= turns; turn++ {
				s.AppendTurn("u", "s",
					schema.UserMessage(fmt.Sprintf("问题%d", turn)),
					schema.AssistantMessage(fmt.Sprintf("回答%d", turn), nil))
			}
			messages := s.Load("u", "s")
			keptTurns := min(turns, 10)
			if len(messages) != keptTurns*2 {
				t.Fatalf("消息数量 = %d，期望 %d", len(messages), keptTurns*2)
			}
			firstTurn := turns - keptTurns + 1
			// 检查每一轮的内容和顺序，而不只检查最终长度。
			for offset := 0; offset < keptTurns; offset++ {
				turn := firstTurn + offset
				user := messages[offset*2]
				assistant := messages[offset*2+1]
				if user == nil || user.Role != schema.User || user.Content != fmt.Sprintf("问题%d", turn) {
					t.Errorf("保留的第%d轮用户消息不正确: %+v", turn, user)
				}
				if assistant == nil || assistant.Role != schema.Assistant || assistant.Content != fmt.Sprintf("回答%d", turn) {
					t.Errorf("保留的第%d轮助手消息不正确: %+v", turn, assistant)
				}
			}
		})
	}
}

func TestStore_NilMessagesLeaveHistoryUnchanged(t *testing.T) {
	tests := []struct {
		name      string
		user      *schema.Message
		assistant *schema.Message
	}{
		{name: "用户消息为nil", assistant: schema.AssistantMessage("回答", nil)},
		{name: "助手消息为nil", user: schema.UserMessage("问题")},
		{name: "两条均为nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore()
			// 先填满10轮，确保无效输入也不会触发删除旧历史。
			for turn := 1; turn <= 10; turn++ {
				s.AppendTurn("u", "s",
					schema.UserMessage(fmt.Sprintf("问题%d", turn)),
					schema.AssistantMessage(fmt.Sprintf("回答%d", turn), nil))
			}
			before := s.Load("u", "s")
			s.AppendTurn("u", "s", tt.user, tt.assistant)
			after := s.Load("u", "s")
			if !reflect.DeepEqual(before, after) {
				t.Fatal("无效输入改变了原有历史")
			}
		})
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	for _, sameSession := range []bool{true, false} {
		name := "不同会话"
		if sameSession {
			name = "同一会话"
		}
		t.Run(name, func(t *testing.T) {
			const workers = 8
			s := NewStore()
			var wg sync.WaitGroup
			start := make(chan struct{})
			for worker := 0; worker < workers; worker++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					<-start
					sessionID := "shared"
					if !sameSession {
						sessionID = fmt.Sprintf("session-%d", id)
					}
					content := fmt.Sprintf("消息%d", id)
					s.AppendTurn("u", sessionID,
						schema.UserMessage(content), schema.AssistantMessage(content, nil))
					_ = s.Load("u", sessionID)
				}(worker)
			}
			close(start)
			wg.Wait()

			// 并发执行顺序不确定，只检查消息完整、成对且没有丢失。
			if sameSession {
				messages := s.Load("u", "shared")
				if len(messages) != workers*2 {
					t.Fatalf("并发写入后消息数量 = %d，期望 %d", len(messages), workers*2)
				}
				seen := make(map[string]bool)
				for i := 0; i < len(messages); i += 2 {
					user, assistant := messages[i], messages[i+1]
					if user == nil || assistant == nil || user.Role != schema.User || assistant.Role != schema.Assistant || user.Content != assistant.Content {
						t.Fatalf("第%d组消息没有保持完整配对", i/2+1)
					}
					if seen[user.Content] {
						t.Fatalf("重复保存了消息 %q", user.Content)
					}
					seen[user.Content] = true
				}
				for worker := 0; worker < workers; worker++ {
					if !seen[fmt.Sprintf("消息%d", worker)] {
						t.Errorf("缺少第%d个写入任务的消息", worker)
					}
				}
			} else {
				for worker := 0; worker < workers; worker++ {
					messages := s.Load("u", fmt.Sprintf("session-%d", worker))
					content := fmt.Sprintf("消息%d", worker)
					if len(messages) != 2 {
						t.Fatalf("会话%d的消息数量 = %d，期望 2", worker, len(messages))
					}
					if messages[0] == nil || messages[1] == nil || messages[0].Content != content || messages[1].Content != content {
						t.Errorf("会话%d的消息不正确", worker)
					}
				}
			}
		})
	}
}
