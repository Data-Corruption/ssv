package server

import (
	"context"
	"fmt"
	"net/http"
	"ssv/go/database/config"
	"ssv/go/system/sdnotify"

	"github.com/Data-Corruption/stdx/xhttp"
	"github.com/Data-Corruption/stdx/xlog"
)

type urlPrefixCtxKey struct{}

// format: https://example.com:port/ :port being omitted if 80/443
func UrlPrefixIntoContext(ctx context.Context, urlPrefix string) context.Context {
	return context.WithValue(ctx, urlPrefixCtxKey{}, urlPrefix)
}

// format: https://example.com:port/ :port being omitted if 80/443
func UrlPrefixFromContext(ctx context.Context) string {
	if urlPrefix, ok := ctx.Value(urlPrefixCtxKey{}).(string); ok {
		return urlPrefix
	}
	return ""
}

func New(ctx context.Context, handler http.Handler) (*xhttp.Server, error) {
	// get http server related stuff from config
	port, err := config.Get[int](ctx, "port")
	if err != nil {
		return nil, fmt.Errorf("failed to get port from config: %w", err)
	}
	urlPrefix := UrlPrefixFromContext(ctx)
	if urlPrefix == "" {
		xlog.Warnf(ctx, "urlPrefix not set in context, defaulting to localhost")
		urlPrefix = fmt.Sprintf("http://localhost:%d/", port)
	}
	// create http server
	var srv *xhttp.Server
	srv, err = xhttp.NewServer(&xhttp.ServerConfig{
		Addr:    fmt.Sprintf(":%d", port),
		UseTLS:  false,
		Handler: handler,
		AfterListen: func() {
			// tell systemd we're ready
			status := fmt.Sprintf("Listening on %s", srv.Addr())
			if err := sdnotify.Ready(status); err != nil {
				xlog.Warnf(ctx, "sd_notify READY failed: %v", err)
			}
			fmt.Printf("Server is listening on %s\n", urlPrefix)
		},
		OnShutdown: func() {
			// tell systemd we’re stopping
			if err := sdnotify.Stopping("Shutting down"); err != nil {
				xlog.Debugf(ctx, "sd_notify STOPPING failed: %v", err)
			}
			fmt.Println("shutting down, cleaning up resources ...")
		},
	})
	return srv, err
}
