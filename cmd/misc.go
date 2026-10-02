package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"

	"github.com/mrflobow/newsletteraway/internal/config"
)

var providersCmd = &cobra.Command{
	Use:   "providers",
	Short: "List the built-in IMAP provider presets",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "PROVIDER\tHOST\tPORT\tSECURITY\tNOTE")
		for _, n := range config.ProviderNames() {
			p := config.Providers[n]
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", p.Name, p.Host, p.Port, p.Security, p.Note)
		}
		return tw.Flush()
	},
}

var forceInit bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage the configuration file",
}

var configInitCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Write an example config file (mode 0600)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := configPath
		if len(args) == 1 {
			path = args[0]
		}
		if path == "" {
			path = config.DefaultPath()
		}
		if _, err := os.Stat(path); err == nil && !forceInit {
			return fmt.Errorf("%s already exists (use --force to overwrite)", path)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(config.Example), 0o600); err != nil {
			return err
		}
		// WriteFile keeps the mode of an existing file; enforce 0600.
		if err := os.Chmod(path, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote example config to %s\n", path)
		return nil
	},
}

var keychainCmd = &cobra.Command{
	Use:   "keychain",
	Short: "Store or remove account passwords in the OS keychain",
}

var keychainSetCmd = &cobra.Command{
	Use:   "set <account>",
	Short: "Prompt for a password and store it in the keychain under the account name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pw, err := config.PromptPassword(fmt.Sprintf("App password for %s: ", args[0]))
		if err != nil {
			return err
		}
		if pw == "" {
			return errors.New("empty password")
		}
		if err := keyring.Set(config.KeyringService, args[0], pw); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "stored password for %q (set \"password_source\": \"keychain\" on the account)\n", args[0])
		return nil
	},
}

var keychainDeleteCmd = &cobra.Command{
	Use:   "delete <account>",
	Short: "Remove an account password from the keychain",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := keyring.Delete(config.KeyringService, args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "deleted password for %q\n", args[0])
		return nil
	},
}

func init() {
	configInitCmd.Flags().BoolVarP(&forceInit, "force", "f", false, "overwrite an existing file")
	configCmd.AddCommand(configInitCmd)
	keychainCmd.AddCommand(keychainSetCmd, keychainDeleteCmd)
	rootCmd.AddCommand(providersCmd, configCmd, keychainCmd)
}
