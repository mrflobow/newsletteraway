package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/mrflobow/newsletteraway/internal/config"
	"github.com/mrflobow/newsletteraway/internal/imapconn"
	"github.com/mrflobow/newsletteraway/internal/oauth"
	"github.com/mrflobow/newsletteraway/internal/report"
	"github.com/mrflobow/newsletteraway/internal/scanner"
	"github.com/mrflobow/newsletteraway/internal/track"
	"github.com/mrflobow/newsletteraway/internal/unsub"
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
	unsubscribe  bool
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
  newsletteraway scan --account gmail --move-to Newsletters --create-folder --dry-run
  newsletteraway scan --account gmail --unsubscribe       # pick senders for 1-click unsubscribe
  newsletteraway scan --account gmail --unsubscribe -y    # unsubscribe from all 1-click senders`,
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
	f.BoolVarP(&sf.yes, "yes", "y", false, "do not ask for confirmation before moving or unsubscribing")
	f.BoolVar(&sf.dryRun, "dry-run", false, "with --move-to or --unsubscribe: only show what would happen")
	f.BoolVar(&sf.unsubscribe, "unsubscribe", false, "send RFC 8058 one-click unsubscribe requests (asks which senders)")
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
	if sf.createFolder && sf.moveTo == "" {
		return errors.New("--create-folder requires --move-to")
	}
	if sf.dryRun && sf.moveTo == "" && !sf.unsubscribe {
		return errors.New("--dry-run requires --move-to or --unsubscribe")
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
	store, err := track.Load(track.DefaultPath())
	if err != nil {
		return err
	}
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
		j.report.Groups = store.Apply(a.Name, j.report.Groups, time.Now())
		j.report.Detected = 0
		for _, g := range j.report.Groups {
			j.report.Detected += g.Count
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
		err = report.WriteTableStyled(out, reports, outStyle(out))
	}
	if err != nil {
		return err
	}

	if sf.moveTo != "" {
		if err := moveStep(jobs); err != nil {
			return err
		}
	}
	if sf.unsubscribe {
		if err := unsubStep(jobs, store); err != nil {
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
	opts := scanner.Options{
		Since:    since,
		Limit:    limit,
		BodyScan: bodyScan,
		GroupBy:  report.GroupBy(sf.groupBy),
	}
	if !sf.quiet {
		opts.Log = os.Stderr
		if st := report.StyleFor(os.Stderr); st.Width > 0 {
			p := &progress{w: os.Stderr, st: st}
			opts.Log = clearingWriter{p: p, w: os.Stderr}
			opts.Progress = p.update
		}
	}
	return opts, nil
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
	if inner := opts.Progress; inner != nil {
		opts.Progress = func(label string, done, total int) { inner(j.account.Name+"/"+label, done, total) }
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

// stdin and isTTY are replaced by tests.
var (
	stdin io.Reader = os.Stdin
	isTTY           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
)

// unsubStep offers every one-click group for unsubscribing: the user picks
// numbers (or -y takes all), then the POSTs are sent and recorded in store.
func unsubStep(jobs []*job, store *track.Store) error {
	in := bufio.NewReader(stdin)
	es := report.StyleFor(os.Stderr)
	failures := 0
	for _, j := range jobs {
		var cands, manual []report.Group
		for _, g := range j.report.Groups {
			switch {
			case g.Manual:
				manual = append(manual, g)
			case g.OneClick && oneClickURL(g) != "":
				cands = append(cands, g)
			}
		}
		if len(manual) > 0 {
			fmt.Fprintf(os.Stderr, "\n%s: one-click failed recently for %s sender(s), unsubscribe by hand (automatic retry after %d days):\n",
				es.Cyan(j.account.Name), es.Bold(strconv.Itoa(len(manual))), int(track.RetryAfter.Hours()/24))
			for _, g := range manual {
				printManual(es, g)
			}
		}
		if len(cands) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n%s: one-click unsubscribe available for %s sender(s):\n", es.Cyan(j.account.Name), es.Bold(strconv.Itoa(len(cands))))
		// fixed part of a row: "  NN  " + " NNNN mails  last YYYY-MM-DD"
		keyW := 40
		if es.Width > 0 {
			keyW = max(es.Width-37, 16)
		}
		for i, g := range cands {
			fmt.Fprintf(os.Stderr, "  %s  %s %4d mails  last %s\n", es.Bold(fmt.Sprintf("%2d", i+1)),
				report.Fit(report.Clean(g.Key), keyW), g.Count, g.Received.Format("2006-01-02"))
		}
		chosen := cands
		if sf.dryRun {
			continue
		}
		if !sf.yes {
			if !isTTY() {
				return errors.New("refusing to unsubscribe without confirmation on a non-interactive stdin; pass -y")
			}
			fmt.Fprint(os.Stderr, es.Bold(`Unsubscribe from which? (e.g. 1,3 or 1-3, "all", Enter = cancel): `))
			ans, _ := in.ReadString('\n')
			idx, err := parseSelection(ans, len(cands))
			if err != nil {
				return err
			}
			if len(idx) == 0 {
				fmt.Fprintln(os.Stderr, "skipped")
				continue
			}
			chosen = nil
			for _, i := range idx {
				chosen = append(chosen, cands[i])
			}
		}
		for n, g := range chosen {
			tag := fmt.Sprintf("[%d/%d]", n+1, len(chosen))
			transient(es, tag+" requesting "+g.Key+" ...")
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second) // two attempts
			err := unsub.Post(ctx, oneClickURL(g))
			cancel()
			if err != nil {
				failures++
				fmt.Fprintf(os.Stderr, "%s  %s %s: %s\n", clearPrefix(es), tag, es.Red("FAILED"), report.Clean(g.Key)+" - "+report.Clean(err.Error()))
				printManual(es, g)
				store.AddFailure(j.account.Name, g.Key, time.Now(), failureReason(err))
				continue
			}
			store.Add(j.account.Name, g.Key, time.Now())
			fmt.Fprintf(os.Stderr, "%s  %s %s     %s\n", clearPrefix(es), tag, es.Green("OK"), report.Clean(g.Key))
		}
	}
	if err := store.Save(); err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("%d unsubscribe request(s) failed", failures)
	}
	return nil
}

// printManual shows the link to open in a browser; never cut, so it stays copyable.
func printManual(es report.Style, g report.Group) {
	fmt.Fprintf(os.Stderr, "      %s  %s\n", report.Clean(g.Key), es.Dim(report.Clean(oneClickURL(g))))
}

// failureReason is the error without the request URL (which carries a token),
// so the tracking file does not keep it.
func failureReason(err error) string {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		err = ue.Err
	}
	return err.Error()
}

// oneClickURL is the https link of the group (SortLinks puts https first).
func oneClickURL(g report.Group) string {
	for _, l := range g.Links {
		if strings.HasPrefix(strings.ToLower(l), "https:") {
			return l
		}
	}
	return ""
}

// parseSelection turns "1,3", "2-4" or "all" into sorted 0-based indices
// below n. An empty answer selects nothing.
func parseSelection(s string, n int) ([]int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil, nil
	}
	seen := map[int]bool{}
	if s == "all" {
		for i := 0; i < n; i++ {
			seen[i] = true
		}
	}
	for _, part := range strings.Split(s, ",") {
		if s == "all" {
			break
		}
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		if err == nil && isRange {
			b, err = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err != nil || a < 1 || b < a || b > n {
			return nil, fmt.Errorf("invalid selection %q (use numbers 1-%d, e.g. 1,3 or 2-4, or all)", part, n)
		}
		for i := a; i <= b; i++ {
			seen[i-1] = true
		}
	}
	idx := make([]int, 0, len(seen))
	for i := range seen {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	return idx, nil
}

// clearPrefix wipes a pending transient line (terminal only).
func clearPrefix(st report.Style) string {
	if st.Width > 0 {
		return clearLine
	}
	return ""
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
