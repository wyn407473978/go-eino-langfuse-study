package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

// Done被调用时发出信号，帮助测试确认等待者已经到达select。
// 用同步信号组织并发顺序，避免用Sleep猜测另一个goroutine的进度。
type lockWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *lockWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestStore_LockSessionSerializesAndAllowsHistoryAccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := NewStore()
	release, err := s.LockSession(ctx, "u", "s")
	if err != nil {
		t.Fatal(err)
	}
	// 清理函数和正常流程都可能执行释放，用Once保证只释放一次。
	var releaseOnce sync.Once
	releaseFirst := func() { releaseOnce.Do(release) }
	defer releaseFirst()
	waitCtx := &lockWaitContext{Context: ctx, waiting: make(chan struct{})}
	secondResult := make(chan error, 1)
	go func() {
		unlock, err := s.LockSession(waitCtx, "u", "s")
		if err == nil {
			defer unlock()
			if len(s.Load("u", "s")) != 2 {
				err = errors.New("后续请求没有读到前一轮保存的历史")
			}
		}
		secondResult <- err
	}()
	select {
	case <-waitCtx.waiting:
	case <-ctx.Done():
		t.Fatal("等待者没有到达select")
	}
	select {
	case err := <-secondResult:
		t.Fatalf("第一个请求释放前，第二个请求已结束: %v", err)
	default:
	}

	// 回归验证：B等待时不能持有全局mu，否则A无法保存历史。
	saved := make(chan struct{})
	go func() {
		s.AppendTurn("u", "s", schema.UserMessage("问题"), schema.AssistantMessage("回答", nil))
		_ = s.Load("u", "s")
		close(saved)
	}()
	select {
	case <-saved:
		if ctx.Err() != nil {
			t.Fatal("历史操作被阻塞到等待超时")
		}
	case <-ctx.Done():
		t.Fatal("等待会话锁挡住了历史读写，可能仍持有全局mu")
	}
	releaseFirst()
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("释放后第二个请求仍无法获得会话锁")
	}
}

func TestStore_LockSessionOtherKeysCanProceed(t *testing.T) {
	for _, key := range []SessionKey{
		{UserID: "u", SessionID: "other-session"},
		{UserID: "other-user", SessionID: "s"},
	} {
		t.Run(key.UserID+"/"+key.SessionID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := NewStore()
			release, err := s.LockSession(ctx, "u", "s")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			waiterCtx, stopWaiter := context.WithCancel(ctx)
			defer stopWaiter()
			waitCtx := &lockWaitContext{Context: waiterCtx, waiting: make(chan struct{})}
			waiterResult := make(chan error, 1)
			go func() {
				unlock, err := s.LockSession(waitCtx, "u", "s")
				if err == nil {
					unlock()
				}
				waiterResult <- err
			}()
			select {
			case <-waitCtx.waiting:
			case <-ctx.Done():
				t.Fatal("等待者没有启动")
			}
			otherResult := make(chan error, 1)
			go func() {
				unlock, err := s.LockSession(ctx, key.UserID, key.SessionID)
				if err == nil {
					unlock()
				}
				otherResult <- err
			}()
			select {
			case err := <-otherResult:
				if err != nil || ctx.Err() != nil {
					t.Fatalf("独立会话被阻塞: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("一个会话的等待阻塞了其他会话")
			}
			stopWaiter()
			select {
			case err := <-waiterResult:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("取消等待的结果 = %v", err)
				}
			case <-ctx.Done():
				t.Fatal("取消后的等待者未退出")
			}
		})
	}
}

func TestStore_LockSessionWaitingCanBeCanceled(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "主动取消"
		if deadline {
			name = "等待超时"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := NewStore()
			release, err := s.LockSession(ctx, "u", "s")
			if err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			defer once.Do(release)
			var waiterCtx context.Context
			var stopWaiter context.CancelFunc
			wantError := context.Canceled
			if deadline {
				waiterCtx, stopWaiter = context.WithTimeout(ctx, 20*time.Millisecond)
				wantError = context.DeadlineExceeded
			} else {
				waiterCtx, stopWaiter = context.WithCancel(ctx)
			}
			defer stopWaiter()
			waitCtx := &lockWaitContext{Context: waiterCtx, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				unlock, err := s.LockSession(waitCtx, "u", "s")
				if unlock != nil {
					unlock()
				}
				result <- err
			}()
			select {
			case <-waitCtx.waiting:
			case <-ctx.Done():
				t.Fatal("等待者没有启动")
			}
			if !deadline {
				stopWaiter()
			}
			select {
			case err := <-result:
				if !errors.Is(err, wantError) {
					t.Fatalf("等待错误 = %v，期望 %v", err, wantError)
				}
			case <-ctx.Done():
				t.Fatal("等待没有及时结束")
			}
			once.Do(release)
			// 失败的等待不应遗留令牌，后续请求应仍能获得锁。
			unlock, err := s.LockSession(ctx, "u", "s")
			if err != nil {
				t.Fatal("取消等待后会话锁无法再次获得:", err)
			}
			unlock()
		})
	}
}

func TestStore_LockSessionRejectsCanceledContext(t *testing.T) {
	s := NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	unlock, err := s.LockSession(ctx, "u", "s")
	if unlock != nil {
		unlock()
		t.Fatal("已取消的请求获得了会话锁")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("错误 = %v，期望context.Canceled", err)
	}
	validCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	unlock, err = s.LockSession(validCtx, "u", "s")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
