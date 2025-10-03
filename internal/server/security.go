package server

import "sync"

type connectionLimiter struct {
	limit  int
	mu     sync.Mutex
	counts map[string]int
}

func newConnectionLimiter(limit int) *connectionLimiter {
	if limit <= 0 {
		return nil
	}
	return &connectionLimiter{
		limit:  limit,
		counts: make(map[string]int),
	}
}

func (l *connectionLimiter) acquire(ip string) (func(), bool) {
	if l == nil {
		return func() {}, true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	count := l.counts[ip]
	if count >= l.limit {
		return nil, false
	}

	l.counts[ip] = count + 1
	released := false
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if released {
			return
		}
		if current := l.counts[ip]; current <= 1 {
			delete(l.counts, ip)
		} else {
			l.counts[ip] = current - 1
		}
		released = true
	}, true
}
