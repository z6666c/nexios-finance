package tracing

import (
	"context"
	"net/http"

	"nexios-finance/internal/uuid"
)

type ctxKey string

const traceIDKey ctxKey = "trace_id"

const HeaderName = "X-Trace-Id"

func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

func FromContext(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

func NewTraceID() string {
	return uuid.New().String()
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get(HeaderName)
		if traceID == "" {
			traceID = NewTraceID()
		}
		w.Header().Set(HeaderName, traceID)
		ctx := WithTraceID(r.Context(), traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
