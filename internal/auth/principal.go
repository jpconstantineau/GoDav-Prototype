package auth

import "context"

type principalKey struct{}

func WithPrincipal(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, principalKey{}, id)
}
func Principal(ctx context.Context) string { id, _ := ctx.Value(principalKey{}).(string); return id }
