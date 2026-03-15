package id

import (
	"errors"
	"sync"
)

var ErrNoMoreFixedUUID = errors.New("no more fixed uuid")

// UUIDProvider generates internal UUIDv7 strings.
type UUIDProvider interface {
	NewUUIDv7() (string, error)
}

type fixedUUIDProvider struct {
	mu    sync.Mutex
	idx   int
	items []string
}

// NewFixedUUIDProvider returns a deterministic UUID provider for tests.
func NewFixedUUIDProvider(items []string) UUIDProvider {
	copied := make([]string, len(items))
	copy(copied, items)
	return &fixedUUIDProvider{items: copied}
}

func (p *fixedUUIDProvider) NewUUIDv7() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.idx >= len(p.items) {
		return "", ErrNoMoreFixedUUID
	}
	v := p.items[p.idx]
	p.idx++
	return v, nil
}
