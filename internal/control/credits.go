package control

import "sync"

type CreditLedger struct {
	mu      sync.Mutex
	balance int64
}

func NewCreditLedger(initial int64) *CreditLedger {
	return &CreditLedger{balance: initial}
}

func (l *CreditLedger) Balance() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balance
}

func (l *CreditLedger) Spend(v int64) bool {
	if v <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.balance < v {
		return false
	}
	l.balance -= v
	return true
}

func (l *CreditLedger) Earn(v int64) {
	if v <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balance += v
}
