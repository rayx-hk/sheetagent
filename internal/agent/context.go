package agent

import (
	"context"

	"github.com/rayx-hk/dataagent/internal/executor"
)

type contextKey struct{}

var answerPositionKey = contextKey{}
var replSessionKey = contextKey{}

// WithREPLSession returns a context with the REPL session for stateful Python execution.
func WithREPLSession(ctx context.Context, session executor.REPLSession) context.Context {
	return context.WithValue(ctx, replSessionKey, session)
}

// REPLSessionFromContext returns the REPL session from context, or nil if not set.
func REPLSessionFromContext(ctx context.Context) executor.REPLSession {
	s, _ := ctx.Value(replSessionKey).(executor.REPLSession)
	return s
}
