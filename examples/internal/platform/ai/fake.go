package ai

import (
	"context"
	"fmt"
)

// Fake is what application tests use. No test in the normal suite touches a
// provider: that would be slow, flaky, costly and non-deterministic.
type Fake struct {
	Responses map[string]*Response // keyed by prompt name
	Embedding []float32
	Err       error
	Calls     []Request
}

func (f *Fake) Complete(_ context.Context, req Request) (*Response, error) {
	f.Calls = append(f.Calls, req)
	if f.Err != nil {
		return nil, f.Err
	}
	if r, ok := f.Responses[req.PromptName]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("ai.Fake: no response configured for prompt %q", req.PromptName)
}

func (f *Fake) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.Embedding
	}
	return out, nil
}

var _ Provider = (*Fake)(nil)
