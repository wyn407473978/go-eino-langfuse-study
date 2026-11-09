package memory

import (
	"context"
	"github.com/cloudwego/eino/schema"
	"sync"
)

type SessionKey struct {
	UserID    string
	SessionID string
}

type Store struct {
	mu sync.Mutex

	history      map[SessionKey][]*schema.Message
	sessionLocks map[SessionKey]chan struct{}
}

func NewStore() *Store {
	return &Store{
		history:      make(map[SessionKey][]*schema.Message),
		sessionLocks: make(map[SessionKey]chan struct{}),
	}
}

func (s *Store) LockSession(ctx context.Context, userID, sessionID string) (func(), error) {

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	ch, exists := s.sessionLocks[SessionKey{UserID: userID, SessionID: sessionID}]
	if !exists {
		ch = make(chan struct{}, 1)
		s.sessionLocks[SessionKey{UserID: userID, SessionID: sessionID}] = ch
	}
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-ch
			return nil, err
		}
		return func() {
			<-ch
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}

}

func (s *Store) Load(userID, sessionID string) []*schema.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := s.history[SessionKey{userID, sessionID}]
	result := make([]*schema.Message, len(messages))
	copy(result, messages)
	return result
}

func (s *Store) AppendTurn(userID, SessionID string, userMessage, assistantMessage *schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if userMessage != nil && assistantMessage != nil {
		messages := s.history[SessionKey{userID, SessionID}]
		messages = append(messages, userMessage, assistantMessage)
		if len(messages) >= 20 {
			messages = messages[len(messages)-20:]
		}
		s.history[SessionKey{userID, SessionID}] = messages
	}
}
