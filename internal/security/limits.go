package security

import "context"

// Gate bounds the number of queries executing at once.
type Gate struct {
	sem chan struct{}
}

func NewGate(n int) *Gate {
	if n < 1 {
		n = 1
	}
	return &Gate{sem: make(chan struct{}, n)}
}

// Acquire blocks until a slot is free or ctx is cancelled.
func (g *Gate) Acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case g.sem <- struct{}{}:
		return nil
	}
}

// Release returns a slot. It must be paired with a successful Acquire.
func (g *Gate) Release() {
	<-g.sem
}
