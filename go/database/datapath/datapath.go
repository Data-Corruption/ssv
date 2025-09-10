package datapath

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

type ctxKey struct{}

func IntoContext(ctx context.Context, path string) context.Context {
	return context.WithValue(ctx, ctxKey{}, path)
}

func FromContext(ctx context.Context) string {
	if path, ok := ctx.Value(ctxKey{}).(string); ok {
		return path
	}
	return ""
}

// Get returns the data path for the application.
// Assumes CGO is enabled.
func Get(appName string) (string, error) {
	// non-root: use current user's home.
	if os.Geteuid() != 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home dir: %w", err)
		}
		return filepath.Join(home, "."+appName), nil
	}

	// root: require an invoking non-root user (sudo/doas).
	home, err := invokingUserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+appName), nil
}

func invokingUserHome() (string, error) {
	// prefer UID (avoids name ambiguities).
	if uid := firstNonEmpty(os.Getenv("SUDO_UID"), os.Getenv("DOAS_UID")); uid != "" && uid != "0" {
		u, err := user.LookupId(uid)
		if err != nil {
			return "", fmt.Errorf("cannot lookup uid %s: %w", uid, err)
		}
		if u.HomeDir == "" {
			return "", fmt.Errorf("empty home for uid %s", uid)
		}
		return u.HomeDir, nil
	}

	// fallback to username if UID not present.
	if uname := firstNonEmpty(os.Getenv("SUDO_USER"), os.Getenv("DOAS_USER")); uname != "" {
		u, err := user.Lookup(uname)
		if err != nil {
			return "", fmt.Errorf("cannot lookup user %q: %w", uname, err)
		}
		if u.Uid == "0" {
			return "", errors.New("invoking user resolves to root; aborting")
		}
		if u.HomeDir == "" {
			return "", fmt.Errorf("empty home for user %q", uname)
		}
		return u.HomeDir, nil
	}

	return "", errors.New("refusing to run as real root: no SUDO_*/DOAS_* env present")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
