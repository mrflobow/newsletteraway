// Package cmd implements the newsletteraway command line interface.
package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/mrflobow/newsletteraway/internal/config"
)

var configPath string

var rootCmd = &cobra.Command{
	Use:   "newsletteraway",
	Short: "Find newsletters in IMAP mailboxes and list their unsubscribe links",
	Long: `newsletteraway scans IMAP mailboxes (read-only by default) for newsletters,
detected via the List-Unsubscribe, List-Id and Precedence headers and optionally
the message body, and lists the unsubscribe links grouped by sender.`,
	SilenceUsage: true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to JSON config (default "+config.DefaultPath()+")")
}

// loadConfig loads the explicit --config file, or the default file if it
// exists. It returns (nil, false, nil) when no config is available.
func loadConfig() (*config.File, bool, error) {
	path, explicit := configPath, configPath != ""
	if !explicit {
		path = config.DefaultPath()
	}
	f, err := config.Load(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return nil, false, nil
	}
	if err != nil {
		return nil, explicit, err
	}
	if w := config.PermissionWarning(path, f); w != "" {
		fmt.Fprintln(os.Stderr, w)
	}
	return f, explicit, nil
}
