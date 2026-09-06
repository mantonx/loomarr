package recordfixture

import "sync"

// Recorder is a shared, concurrency-safe recorded-call fixture. Respond may
// inspect each input and choose its result; nil Respond returns the zero result.
type Recorder[Input any, Output any] struct {
	Respond func(Input) (Output, error)

	mu     sync.Mutex
	inputs []Input
}

// Call records input before returning the configured response.
func (r *Recorder[Input, Output]) Call(input Input) (Output, error) {
	r.mu.Lock()
	r.inputs = append(r.inputs, input)
	r.mu.Unlock()
	if r.Respond == nil {
		var zero Output
		return zero, nil
	}
	return r.Respond(input)
}

// Inputs returns the recorded call inputs in invocation order.
func (r *Recorder[Input, Output]) Inputs() []Input {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Input(nil), r.inputs...)
}

// Calls returns the number of invocations.
func (r *Recorder[Input, Output]) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inputs)
}
