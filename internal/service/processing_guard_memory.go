package service

import (
	"errors"
	"strings"
	"sync"
)

var ErrProcessingGuardUnavailable = errors.New("processing guard unavailable")

type StageProcessingGuard interface {
	TryBegin(txnNo, stage string) bool
}

func ProcessingKey(txnNo, stage string) string {
	return strings.TrimSpace(txnNo) + "+" + strings.TrimSpace(stage)
}

type ProcessingGuard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewProcessingGuard() *ProcessingGuard {
	return &ProcessingGuard{seen: map[string]struct{}{}}
}

func (g *ProcessingGuard) TryBegin(txnNo, stage string) bool {
	key := ProcessingKey(txnNo, stage)
	if key == "+" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = struct{}{}
	return true
}
