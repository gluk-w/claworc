package proxy

import "context"

type contextKey string

const instanceNameKey contextKey = "instanceName"

func withInstanceName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, instanceNameKey, name)
}

func GetInstanceName(ctx context.Context) string {
	if v, ok := ctx.Value(instanceNameKey).(string); ok {
		return v
	}
	return ""
}
