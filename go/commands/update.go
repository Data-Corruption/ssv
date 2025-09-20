//go:build linux

package commands

import (
	"context"
	"fmt"
	"ssv/go/database/config"
	"ssv/go/system/update"

	"github.com/urfave/cli/v3"
)

var Update = &cli.Command{
	Name:  "update",
	Usage: "update the application",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return update.Update(ctx, false)
	},
}

var UpdateToggleNotify = &cli.Command{
	Name:  "update-toggle-notify",
	Usage: "toggle update notifications",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		// get
		updateNotify, err := config.Get[bool](ctx, "updateNotify")
		if err != nil {
			return fmt.Errorf("failed to get updateNotify from config: %w", err)
		}
		// set
		if err := config.Set(ctx, "updateNotify", !updateNotify); err != nil {
			return fmt.Errorf("failed to set updateNotify in config: %w", err)
		}
		if !updateNotify {
			fmt.Println("Update notifications are now enabled.")
		} else {
			fmt.Println("Update notifications are now disabled.")
		}
		return nil
	},
}
