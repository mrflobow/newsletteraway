package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mrflobow/newsletteraway/internal/config"
	"github.com/mrflobow/newsletteraway/internal/imapconn"
	"github.com/mrflobow/newsletteraway/internal/oauth"
	"github.com/mrflobow/newsletteraway/internal/report"
	"github.com/mrflobow/newsletteraway/internal/scanner"
)

type scanFlags struct {
	accounts     []string
	provider     string
	host         string
	port         int
	security     string
	user         string
	passwordEnv  string
	keychain     bool
	auth         string
	oauthClient  string
	oauthTenant  string
	mailboxes    []string
	days         int
	since        string
	limit        int
	bodyScan     bool
	groupBy      string
	output       string
	quiet        bool
	moveTo       string
	createFolder bool
	yes          bool
	dryRun       bool
}

var sf scanFlags

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan mailboxes for newsletters and print unsubscribe links",
	Example: `  newsletteraway scan                                   # all accounts from the config
  newsletteraway scan --account gmail --days 30
  newsletteraway scan --provider gmail --user me@gmail.com --password-env GMAIL_APP_PW
  newsletteraway scan --provider icloud --user me@icloud.com --output json
  newsletteraway scan --provider outlook --user me@outlook.com --auth oauth2 --oauth2-client-id <app-id>
  newsletteraway scan --account gmail --move-to Newsletters --create-folder --dry-run`,
	Args: cobra.NoArgs,
	RunE: runScan,
}

func init() {
	f := scanCmd.Flags()
	f.StringSliceVarP(&sf.accounts, "account", "a", nil, "account name(s) from the config to scan (default: all)")
	f.StringVarP(&sf.provider, "provider", "p", "", "IMAP preset: "+strings.Join(config.ProviderNames(), ", ")+", or custom")
	f.StringVar(&sf.host, "host", "", "IMAP host (overrides the preset)")
	f.IntVar(&sf.port, "port", 0, "IMAP port (default 993 for tls, 143 otherwise)")
	f.StringVar(&sf.security, "security", "", "connection security: tls, starttls or none")
	f.StringVarP(&sf.user, "user", "u", "", "IMAP username")
	f.StringVar(&sf.passwordEnv, "password-env", "", "name of the environment variable holding the password")
	f.BoolVar(&sf.keychain, "keychain", false, "read the password from the OS keychain")
	f.StringVar(&sf.auth, "auth", "", "login method: password (default) or oauth2 (outlook)")
	f.StringVar(&sf.oauthClient, "oauth2-client-id", "", "application (client) ID of your Microsoft app registration")
	f.StringVar(&sf.oauthTenant, "oauth2-tenant", "", "Microsoft tenant: common (default), consumers, organizations or a tenant ID")
	f.StringSliceVarP(&sf.mailboxes, "mailbox", "m", nil, "mailbox(es) to scan (default INBOX)")
	f.IntVarP(&sf.days, "days", "d", config.DefaultDays, "only scan messages from the last N days (0 = all)")
	f.StringVar(&sf.since, "since", "", "only scan messages since YYYY-MM-DD (overrides --days)")
	f.IntVarP(&sf.limit, "limit", "n", 0, "max messages per mailbox, newest first (0 = unlimited)")
	f.BoolVar(&sf.bodyScan, "body-scan", false, "also search message bodies for unsubscribe links when headers have none (slower)")
	f.StringVar(&sf.groupBy, "group-by", string(report.BySender), "group results by: sender or domain")
	f.StringVarP(&sf.output, "output", "o", "table", "output format: table or json")
	f.BoolVarP(&sf.quiet, "quiet", "q", false, "suppress progress messages on stderr")
	f.StringVar(&sf.moveTo, "move-to", "", "move detected newsletters to this folder (Gmail: label)")
	f.BoolVar(&sf.createFolder, "create-folder", false, "create the --move-to folder if missing")
	f.BoolVarP(&sf.yes, "yes", "y", false, "do not ask for confirmation before moving")
	f.BoolVar(&sf.dryRun, "dry-run", false, "with --move-to: only show what would be moved")
	rootCmd.AddCommand(scanCmd)
}

// job is a resolved account plus the data needed for the optional move step.
type job struct {
	account config.Account
	cred    *imapconn.Credentials // nil until resolved
	report  report.AccountReport
}

func runScan(cmd *cobra.Command, _ []string) error {
	if sf.output != "table" && sf.output != "json" {
		return fmt.Errorf("invalid --output %q (table, json)", sf.output)
	}
	if sf.groupBy != string(report.BySender) && sf.groupBy != string(report.ByDomain) {
		return fmt.Errorf("invalid --group-by %q (sender, domain)", sf.groupBy)
	}
	if (sf.dryRun || sf.createFolder) && sf.moveTo == "" {
		return errors.New("--dry-run and --create-folder require --move-to")
	}

	overrides := config.Overrides{
		Provider:    sf.provider,
		Host:        sf.host,
		Port:        sf.port,
		Security:    config.Security(sf.security),
		Username:    sf.user,
		PasswordEnv: sf.passwordEnv,
		Keychain:    sf.keychain,
		Mailboxes:   sf.mailboxes,

		Auth:           config.Auth(sf.auth),
		OAuth2ClientID: sf.oauthClient,
		OAuth2Tenant:   sf.oauthTenant,
	}

	cfg, explicit, err := loadConfig()
	if err != nil {
		return err
	}
	// Connection flags without --config/--account mean an ad-hoc account,
	// even if a default config file exists.
	if !explicit && len(sf.accounts) == 0 && overrides.HasConnection() {
		cfg = nil
	}
	var defaults config.Defaults
	if cfg != nil {
		defaults = cfg.Defaults
	}

	opts, err := scanOptions(cmd, defaults)
	if err != nil {
		return err
	}

	selected, err := config.Select(cfg, sf.accounts, overrides)
	if err != nil {
		return err
	}
	var accounts []config.Account
	for _, a := range selected {
		ra, err := config.Resolve(a, defaults, overrides)
		if err != nil {
			return err
		}
		accounts = append(accounts, ra)
	}

	secrets := config.DefaultSecretResolver()
	var (
		jobs   []*job
		failed int
	)
	for _, a := range accounts {
		j := &job{account: a, report: report.AccountReport{Account: a.Name, Mailboxes: a.Mailboxes}}
		jobs = append(jobs, j)
		if err := scanAccount(j, secrets, opts); err != nil {
			j.report.Error = err.Error()
			failed++
			if sf.output == "json" { // the table shows errors inline
				fmt.Fprintf(os.Stderr, "error: %s: %s\n", a.Name, report.Clean(err.Error()))
			}
		} else if j.report.Error != "" {
			failed++
		}
	}

	reports := make([]report.AccountReport, len(jobs))
	for i, j := range jobs {
		reports[i] = j.report
	}
	out := cmd.OutOrStdout()
	if sf.output == "json" {
		err = report.WriteJSON(out, reports)
	} else {
		err = report.WriteTable(out, reports)
	}
	if err != nil {
		return err
	}

	if sf.moveTo != "" {
		if err := moveStep(jobs); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d account(s) had errors", failed, len(jobs))
	}
	return nil
}

func scanOptions(cmd *cobra.Command, d config.Defaults) (scanner.Options, error) {
	flags := cmd.Flags()
	days := sf.days
	if !flags.Changed("days") && d.Days != 0 {
		days = d.Days
	}
	since, err := config.SinceDate(sf.since, days, time.Now())
	if err != nil {
		return scanner.Options{}, err
	}
	limit := sf.limit
	if !flags.Changed("limit") {
		limit = d.Limit
	}
	bodyScan := sf.bodyScan
	if !flags.Changed("body-scan") {
		bodyScan = d.BodyScan
	}
	var log io.Writer = os.Stderr
	if sf.quiet {
		log = nil
	}
	return scanner.Options{
		Since:    since,
		Limit:    limit,
		BodyScan: bodyScan,
		GroupBy:  report.GroupBy(sf.groupBy),
		Log:      log,
	}, nil
}

func scanAccount(j *job, secrets *config.SecretResolver, opts scanner.Options) error {
	cred, err := credentials(j.account, secrets)
	if err != nil {
		return err
	}
	j.cred = &cred
	if opts.Log != nil {
		fmt.Fprintf(opts.Log, "%s: connecting to %s as %s\n", j.account.Name, j.account.Addr(), j.account.Username)
	}
	conn, err := imapconn.Dial(j.account, cred, imapconn.Options{})
	if err != nil {
		return withProviderHint(j.account, err)
	}
	defer conn.Close()

	if opts.Log != nil {
		opts.Log = prefixWriter{prefix: j.account.Name + "/", w: opts.Log}
	}
	j.report = scanner.Scan(conn, j.account.Name, j.account.Mailboxes, opts)
	return nil
}

func moveStep(jobs []*job) error {
	in := bufio.NewReader(os.Stdin)
	for _, j := range jobs {
		n := j.report.Detected
		if n == 0 || j.cred == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n%s: %d newsletter message(s) from %d sender(s) -> %q\n", j.account.Name, n, len(j.report.Groups), sf.moveTo)
		if sf.dryRun {
			for _, g := range j.report.Groups {
				fmt.Fprintf(os.Stderr, "  would move %3d  %s\n", g.Count, report.Clean(g.Key))
			}
			continue
		}
		if !sf.yes {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("refusing to move without confirmation on a non-interactive stdin; pass --yes")
			}
			fmt.Fprint(os.Stderr, "Move these messages? [y/N] ")
			ans, _ := in.ReadString('\n')
			if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
				fmt.Fprintln(os.Stderr, "skipped")
				continue
			}
		}
		conn, err := imapconn.Dial(j.account, *j.cred, imapconn.Options{})
		if err != nil {
			return fmt.Errorf("%s: %w", j.account.Name, err)
		}
		moved, err := scanner.MoveAll(conn, j.report.Groups, sf.moveTo, sf.createFolder)
		conn.Close()
		fmt.Fprintf(os.Stderr, "%s: moved %d message(s) to %q\n", j.account.Name, moved, sf.moveTo)
		if err != nil {
			return fmt.Errorf("%s: %w", j.account.Name, err)
		}
	}
	return nil
}

// tokenStore holds OAuth2 tokens; tests replace it.
var tokenStore oauth.Store = oauth.KeyringStore{}

// credentials resolves the password or OAuth2 access token for an account.
// OAuth2 sign-in is interactive only when stdin is a terminal.
func credentials(a config.Account, secrets *config.SecretResolver) (imapconn.Credentials, error) {
	if a.Auth == config.AuthOAuth2 {
		var interactive io.Writer
		if term.IsTerminal(int(os.Stdin.Fd())) {
			interactive = os.Stderr
		}
		tok, err := oauth.AccessToken(context.Background(), a, tokenStore, interactive, os.Stderr)
		return imapconn.Credentials{OAuth2Token: tok}, err
	}
	pw, err := secrets.Password(a)
	return imapconn.Credentials{Password: pw}, err
}

func withProviderHint(a config.Account, err error) error {
	if !strings.Contains(err.Error(), "login") {
		return err
	}
	if a.Auth == config.AuthOAuth2 {
		return fmt.Errorf("%w\n  hint: check that IMAP is enabled for the mailbox and that the app registration allows IMAP.AccessAsUser.All; then run: newsletteraway oauth login %s", err, a.Name)
	}
	if p, ok := config.Providers[a.Provider]; ok {
		return fmt.Errorf("%w\n  hint (%s): %s", err, p.Name, p.Note)
	}
	return err
}

// prefixWriter prefixes every write (one progress line) with the account name.
type prefixWriter struct {
	prefix string
	w      io.Writer
}

func (p prefixWriter) Write(b []byte) (int, error) {
	if _, err := io.WriteString(p.w, p.prefix); err != nil {
		return 0, err
	}
	return p.w.Write(b)
}
