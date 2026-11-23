package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Build metadata injected at link time via -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// VersionInfo represents structured build and version metadata.
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// currentVersionInfo returns the current build metadata snapshot.
func currentVersionInfo() VersionInfo {
	return VersionInfo{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}

// NewRootCmd builds and returns the root Cobra command for usage-cli.
func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:          "usage-cli",
		Short:        "Track subscription usage limits and orchestrate safe agent handoffs",
		SilenceUsage: true,
		Version:      Version,
	}

	rootCmd.SetVersionTemplate(fmt.Sprintf("usage-cli %s (commit: %s, date: %s)\n", Version, Commit, Date))
	rootCmd.AddCommand(newVersionCmd())

	return rootCmd
}

// newVersionCmd builds the version subcommand supporting text and JSON output.
func newVersionCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version and build metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := currentVersionInfo()
			out := cmd.OutOrStdout()

			if jsonOutput {
				enc := json.NewEncoder(out)
				return enc.Encode(info)
			}

			_, err := fmt.Fprintf(out, "usage-cli %s (commit: %s, date: %s)\n", info.Version, info.Commit, info.Date)
			return err
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output version information as JSON")

	return cmd
}

// Execute runs the root CLI command and returns any execution error.
func Execute() error {
	return NewRootCmd().Execute()
}
