package limiter

import (
	"errors"
	"sync"
)

type CheckoutResult struct {
	Granted          float64
	Remaining        float64
	ResetTimeSeconds int64
}

type tokenCheckouter interface {
	Checkout(key string, requested float64) (CheckoutResult, error)
	Reset() error
}

type pocket struct {
	mu        sync.Mutex
	tokens    float64
	remaining float64
	resetTime int64
}

type leasingLimiter struct {
	inner     tokenCheckouter
	leaseSize float64
	capacity  float64
	mu        sync.Mutex
	pockets   map[string]*pocket
	flight    singleFlight
}

func newLeasingLimiter(inner tokenCheckouter, leaseSize, capacity float64) (*leasingLimiter, error) {
	if inner == nil {
		return nil, errors.New("lease cache inner limiter cannot be nil")
	}
	if leaseSize <= 1 {
		return nil, errors.New("lease size must be greater than 1")
	}
	return &leasingLimiter{
		inner:     inner,
		leaseSize: leaseSize,
		capacity:  capacity,
		pockets:   make(map[string]*pocket),
	}, nil
}

func (l *leasingLimiter) Allow(key string, cost float64) (AllowResult, error) {
	if key == "" {
		return AllowResult{}, errors.New("key cannot be empty")
	}
	if cost <= 0 {
		return AllowResult{}, errors.New("cost must be positive")
	}
	if l.capacity > 0 && cost > l.capacity {
		return AllowResult{}, errors.New("cost exceeds bucket capacity")
	}

	p := l.pocket(key)
	for {
		if result, ok := p.spend(cost); ok {
			return result, nil
		}

		granted, err := l.refillPocket(key, p, cost)
		if err != nil {
			return AllowResult{}, err
		}
		if result, ok := p.spend(cost); ok {
			return result, nil
		}
		if granted < cost {
			p.mu.Lock()
			defer p.mu.Unlock()
			return AllowResult{
				Allowed:          false,
				Remaining:        remainingTokens(p.tokens),
				ResetTimeSeconds: p.resetTime,
			}, nil
		}
	}
}

func (l *leasingLimiter) Reset() error {
	l.mu.Lock()
	l.pockets = make(map[string]*pocket)
	l.mu.Unlock()
	return l.inner.Reset()
}

func (l *leasingLimiter) pocket(key string) *pocket {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.pockets[key]
	if p == nil {
		p = &pocket{}
		l.pockets[key] = p
	}
	return p
}

func (l *leasingLimiter) refillPocket(key string, p *pocket, cost float64) (float64, error) {
	granted, err := l.flight.Do(key, func() (float64, error) {
		p.mu.Lock()
		if p.tokens >= cost {
			available := p.tokens
			p.mu.Unlock()
			return available, nil
		}
		p.mu.Unlock()

		requested := l.leaseSize
		if cost > requested {
			requested = cost
		}
		result, err := l.inner.Checkout(key, requested)
		if err != nil {
			return 0, err
		}

		p.mu.Lock()
		p.tokens += result.Granted
		p.remaining = result.Remaining
		p.resetTime = result.ResetTimeSeconds
		p.mu.Unlock()
		return result.Granted, nil
	})
	return granted, err
}

func (p *pocket) spend(cost float64) (AllowResult, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tokens < cost {
		return AllowResult{}, false
	}
	p.tokens -= cost
	return AllowResult{
		Allowed:          true,
		Remaining:        remainingTokens(p.tokens + p.remaining),
		ResetTimeSeconds: p.resetTime,
	}, true
}

type singleFlight struct {
	mu sync.Mutex
	in map[string]*flightCall
}

type flightCall struct {
	wg      sync.WaitGroup
	err     error
	granted float64
}

func (s *singleFlight) Do(key string, fn func() (float64, error)) (float64, error) {
	s.mu.Lock()
	if s.in == nil {
		s.in = make(map[string]*flightCall)
	}
	if c, ok := s.in[key]; ok {
		s.mu.Unlock()
		c.wg.Wait()
		return c.granted, c.err
	}
	c := &flightCall{}
	c.wg.Add(1)
	s.in[key] = c
	s.mu.Unlock()

	c.granted, c.err = fn()

	s.mu.Lock()
	delete(s.in, key)
	s.mu.Unlock()
	c.wg.Done()
	return c.granted, c.err
}
