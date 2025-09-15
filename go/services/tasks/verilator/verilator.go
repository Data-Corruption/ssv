package verilator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"ssv/go/database/datapath"

	"github.com/Data-Corruption/stdx/xlog"
	"github.com/urfave/cli/v3"
)

// getPaths returns the verilator root directory and binary path.
func getPaths(ctx context.Context) (string, string, error) {
	dataPath := datapath.FromContext(ctx)
	if dataPath == "" {
		return "", "", fmt.Errorf("data path not set in context")
	}
	verRootDir := filepath.Join(dataPath, "verilator")
	verBin := filepath.Join(verRootDir, "bin", "verilator")
	// TODO: check if verilator binary exists and is executable
	return verRootDir, verBin, nil
}

func Passthrough(ctx context.Context, cmd *cli.Command) error {
	verRootDir, verBin, err := getPaths(ctx)
	if err != nil {
		return err
	}
	command := exec.Command(verBin, os.Args[2:]...)
	command.Env = append(os.Environ(), fmt.Sprintf("VERILATOR_ROOT=%s", verRootDir))
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	xlog.Debugf(ctx, "Executing command: %s", command.String())
	return command.Run()
}
