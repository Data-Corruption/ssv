package app

import (
	"context"
)

type AppData struct {
	Name      string
	Version   string
	UrlPrefix string // format: https://example.com:port/ :port being omitted if 80/443
}

type ctxKey struct{}

func IntoContext(ctx context.Context, appData AppData) context.Context {
	return context.WithValue(ctx, ctxKey{}, appData)
}

func FromContext(ctx context.Context) (AppData, bool) {
	appData, ok := ctx.Value(ctxKey{}).(AppData)
	return appData, ok && (appData != AppData{})
}
