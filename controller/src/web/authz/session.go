package authz

import (
	"IpacPanel/controller/src/msg"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
)

const tokenBytes = 32

type SessionStore interface {
	GetOrCreateUserToken(username string) (string, error)
	ResetUserToken(username string, oldToken string) (string, error)
	RenameUserTokenOwner(oldUsername string, newUsername string)
	RemoveUserToken(username string)
	ValidateBearerToken(token string) (string, bool)
}

// MemorySessionStore preserves the existing single-token-per-user map behavior.
type MemorySessionStore struct {
	mu          sync.RWMutex
	userToToken map[string]string
	tokenToUser map[string]string
}

func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{userToToken: make(map[string]string), tokenToUser: make(map[string]string)}
}

func (s *MemorySessionStore) GetOrCreateUserToken(username string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token, ok := s.userToToken[username]; ok && strings.TrimSpace(token) != "" {
		return token, nil
	}
	token, err := newRandomToken()
	if err != nil {
		return "", err
	}
	s.userToToken[username] = token
	s.tokenToUser[token] = username
	return token, nil
}

func (s *MemorySessionStore) ResetUserToken(username string, oldToken string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", newError(ErrorCodeInvalidToken, msg.InvalidTokenLength, nil)
	}
	token, err := newRandomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if oldToken = strings.TrimSpace(oldToken); oldToken != "" {
		delete(s.tokenToUser, oldToken)
	}
	if currentToken, ok := s.userToToken[username]; ok && strings.TrimSpace(currentToken) != "" {
		delete(s.tokenToUser, currentToken)
	}
	s.userToToken[username] = token
	s.tokenToUser[token] = username
	return token, nil
}

func (s *MemorySessionStore) RenameUserTokenOwner(oldUsername string, newUsername string) {
	oldUsername = strings.TrimSpace(oldUsername)
	newUsername = strings.TrimSpace(newUsername)
	if oldUsername == "" || newUsername == "" || oldUsername == newUsername {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.userToToken[oldUsername]
	if !ok || strings.TrimSpace(token) == "" {
		return
	}
	delete(s.userToToken, oldUsername)
	s.userToToken[newUsername] = token
	s.tokenToUser[token] = newUsername
}

func (s *MemorySessionStore) RemoveUserToken(username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.userToToken[username]
	if !ok || strings.TrimSpace(token) == "" {
		return
	}
	delete(s.userToToken, username)
	delete(s.tokenToUser, token)
}

func (s *MemorySessionStore) ValidateBearerToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	username, ok := s.tokenToUser[token]
	if !ok || strings.TrimSpace(username) == "" {
		return "", false
	}
	return username, true
}

func newRandomToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
