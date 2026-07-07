package keypool

import "sync"

type Pool struct {
	mu   sync.Mutex
	keys []string
	next int
}

func New(keys []string) *Pool {
	copied := append([]string(nil), keys...)
	return &Pool{keys: copied}
}

func (p *Pool) Next() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.keys) == 0 {
		return "", false
	}
	key := p.keys[p.next%len(p.keys)]
	p.next = (p.next + 1) % len(p.keys)
	return key, true
}

func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}
