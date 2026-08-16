package shared

import "context"

type logContextKey struct{}

// ContextWithLog returns a child context carrying the log function for one
// operation. Registry clients use it to keep concurrent dependency logs grouped.
func ContextWithLog(ctx context.Context, log LogFunc) context.Context {
	if log == nil {
		return ctx
	}
	return context.WithValue(ctx, logContextKey{}, log)
}

// LogFromContext returns the operation-specific log function, if one is set.
func LogFromContext(ctx context.Context) LogFunc {
	log, _ := ctx.Value(logContextKey{}).(LogFunc)
	return log
}
