package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mrflobow/newsletteraway/internal/config"
	"github.com/mrflobow/newsletteraway/internal/oauth"
)

var oauthCmd = &cobra.Command{
	Use:   "oauth",
	Short: "Sign in with OAuth2 (Outlook) and manage stored tokens",
}

var oauthLoginCmd = &cobra.Command{
	Use:   "login <account>",
	Short: "Sign in via the device code flow and store the token in the keychain",
	Long: `Signs in a config account that has "auth": "oauth2" using the Microsoft
device code flow: open the printed URL, enter the code, and approve access.
The refresh token is stored in the OS keychain and renewed automatically.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		a, err := oauthAccount(args[0])
		if err != nil {
			return err
		}
		cfg, err := oauth.Config(a)
		if err != nil {
			return err
		}
		tok, err := oauth.DeviceLogin(context.Background(), cfg, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		if err := tokenStore.Save(a.Name, tok.RefreshToken); err != nil {
			return fmt.Errorf("save token: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "signed in; token for %q stored in the keychain\n", a.Name)
		return nil
	},
}

var oauthLogoutCmd = &cobra.Command{
	Use:   "logout <account>",
	Short: "Remove the stored OAuth2 token from the keychain",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := tokenStore.Delete(args[0])
		if errors.Is(err, oauth.ErrNoToken) {
			return fmt.Errorf("no token stored for %q", args[0])
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "removed token for %q\n", args[0])
		return nil
	},
}

// oauthAccount loads a config account and checks that it uses OAuth2.
func oauthAccount(name string) (config.Account, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return config.Account{}, err
	}
	if cfg == nil {
		return config.Account{}, errors.New("no config file found; create one with: newsletteraway config init")
	}
	sel, err := config.Select(cfg, []string{name}, config.Overrides{})
	if err != nil {
		return config.Account{}, err
	}
	a, err := config.Resolve(sel[0], cfg.Defaults, config.Overrides{})
	if err != nil {
		return config.Account{}, err
	}
	if a.Auth != config.AuthOAuth2 {
		return config.Account{}, fmt.Errorf("account %q does not use OAuth2 (set \"auth\": \"oauth2\")", name)
	}
	return a, nil
}

func init() {
	oauthCmd.AddCommand(oauthLoginCmd, oauthLogoutCmd)
	rootCmd.AddCommand(oauthCmd)
}
