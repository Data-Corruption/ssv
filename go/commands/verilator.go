package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"ssv/go/services/tasks/verilator"
	"time"

	"github.com/urfave/cli/v3"
)

var Verilator = &cli.Command{
	Name:  "verilator",
	Usage: "verilator passthrough",
	Action: func(ctx context.Context, cmd *cli.Command) error {
		return verilator.Passthrough(ctx, cmd)
	},
}

var InstallVerilator = &cli.Command{
	Name:  "install-verilator",
	Usage: "install a specific verilator version",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "version",
			Usage:   "the version of verilator to install (e.g. v4.212, v5.006, latest(default))",
			Aliases: []string{"v"},
			Value:   "latest",
		},
		&cli.IntFlag{
			Name:    "threads",
			Usage:   "override the recommended number of threads to use for building",
			Aliases: []string{"t"},
			Value:   0,
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		url := "https://raw.githubusercontent.com/Data-Corruption/ssv/main/scripts/install_verilator.sh"
		pipeline := fmt.Sprintf("curl -fsSL %s | sh -s version=%s threads=%d", url, cmd.String("version"), cmd.Int("threads"))

		iCtx, cancel := context.WithTimeout(ctx, 1*time.Hour)
		defer cancel()

		command := exec.CommandContext(iCtx, "sh", "-c", pipeline)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Run()
	},
}
