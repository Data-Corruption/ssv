package commands

import (
	"context"
	"ssv/go/services/tasks/verilator"

	"github.com/urfave/cli/v3"
)

var Verilator = &cli.Command{
	Name:  "verilator",
	Usage: "verilator passthrough",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return verilator.Passthrough(ctx, cmd)
	},
}
