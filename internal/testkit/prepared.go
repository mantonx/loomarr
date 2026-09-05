package testkit

import (
	"context"
	"sync"

	"github.com/loomarr/loomarr/internal/prepared"
)

// PreparedSourceAccess is the shared SourceAccess test double. It can expose one transient input
// and optionally simulate a source revision changing on an exact open attempt.
type PreparedSourceAccess struct {
	mu         sync.Mutex
	Input      prepared.Input
	FailOnCall int
	calls      int
}

func (a *PreparedSourceAccess) OpenInput(context.Context, prepared.Source) (prepared.Input, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.FailOnCall == a.calls {
		return prepared.Input{}, prepared.ErrSourceChanged
	}
	return a.Input, nil
}

func (a *PreparedSourceAccess) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}
