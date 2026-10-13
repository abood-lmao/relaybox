// Package cli implements the relaybox command line.
//
// Exit codes: 0 success, 1 failure, 2 usage or configuration error. Errors
// go to stderr; command output goes to stdout.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abood-lmao/relaybox/internal/config"
	"github.com/abood-lmao/relaybox/internal/fixture"
	"github.com/abood-lmao/relaybox/internal/logging"
	"github.com/abood-lmao/relaybox/internal/retention"
	"github.com/abood-lmao/relaybox/internal/server"
	"github.com/abood-lmao/relaybox/internal/settings"
	"github.com/abood-lmao/relaybox/internal/storage"
	"github.com/abood-lmao/relaybox/internal/version"
	"github.com/abood-lmao/relaybox/internal/webui"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

const usage = `Relaybox captures, inspects, and replays webhooks.

Usage:
  relaybox <command> [flags]

Commands:
  serve      Start the server: dashboard, API, and capture endpoints
  cleanup    Apply the retention policy to the database now
  export     Write captured requests to a fixture file
  doctor     Check the configuration, database, and port
  version    Print version information
  help       Show help for a command

Settings come from RELAYBOX_* environment variables (see docs/configuration.md).
Flags override the matching variables.

Run 'relaybox help <command>' for the flags of a command.
`

var commandHelp = map[string]string{
	"serve": `Usage: relaybox serve [flags]

Starts the server and runs until interrupted (Ctrl+C or SIGTERM). The
dashboard and API only accept requests from this machine unless
RELAYBOX_ALLOW_REMOTE_DASHBOARD=true and RELAYBOX_DASHBOARD_TOKEN are set.

Flags:
  --host ADDRESS      Address to listen on (RELAYBOX_HOST, default 127.0.0.1)
  --port NUMBER       Port to listen on (RELAYBOX_PORT, default 8787)
  --database PATH     SQLite database file (RELAYBOX_DATABASE_PATH, default ./data/relaybox.db)
  --public-url URL    Base URL used in endpoint URLs (RELAYBOX_PUBLIC_BASE_URL)
`,
	"cleanup": `Usage: relaybox cleanup [flags]

Deletes requests older than the retention period and requests beyond the
stored request limit. Uses the settings saved in the dashboard, falling back
to RELAYBOX_RETENTION_DAYS and RELAYBOX_MAX_STORED_REQUESTS. A running server
applies the same policy every 10 minutes.

Flags:
  --dry-run           Report what would be deleted without deleting anything
  --database PATH     SQLite database file (RELAYBOX_DATABASE_PATH)
`,
	"export": `Usage: relaybox export [flags]

Writes the latest captured requests to a fixture file that can be imported
into any Relaybox endpoint. Current redaction rules are applied again, and
endpoint URLs and sender addresses are left out.

Flags:
  --endpoint NAME|ID  Only export requests from this endpoint
  --limit N           Number of requests, newest first (default 100, at most 1000)
  --out FILE          Output file (default: standard output)
  --no-bodies         Leave request bodies out
  --database PATH     SQLite database file (RELAYBOX_DATABASE_PATH)
`,
	"doctor": `Usage: relaybox doctor [flags]

Checks the configuration, the database, the dashboard files, and whether the
port is free, then summarizes the security settings. Exits with 1 if a check
fails.

Flags:
  --host ADDRESS      Address to check (RELAYBOX_HOST)
  --port NUMBER       Port to check (RELAYBOX_PORT)
  --database PATH     SQLite database file (RELAYBOX_DATABASE_PATH)
`,
	"version": `Usage: relaybox version

Prints the version, commit, build date, Go version, and platform.
`,
	"help": `Usage: relaybox help [command]

Shows general help, or the flags of one command.
`,
}

// Main runs the command line and returns the exit code.
func Main(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	r := &runner{ctx: ctx, env: config.FromEnviron(environ), stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return ExitOK
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return r.serve(rest)
	case "cleanup":
		return r.cleanup(rest)
	case "export":
		return r.export(rest)
	case "doctor":
		return r.doctor(rest)
	case "version", "--version":
		return r.version(rest)
	case "help", "-h", "-help", "--help":
		return r.help(rest)
	case "healthcheck":
		return r.healthcheck(rest)
	}
	fmt.Fprintf(stderr, "relaybox: unknown command %q\nRun 'relaybox help' for usage.\n", cmd)
	return ExitUsage
}

type runner struct {
	ctx    context.Context
	env    map[string]string
	stdout io.Writer
	stderr io.Writer
}

func (r *runner) errorf(format string, args ...any) {
	fmt.Fprintf(r.stderr, "relaybox: "+format+"\n", args...)
}

// flags returns a flag set whose -h output is the command's help text.
func (r *runner) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	fs.Usage = func() {}
	return fs
}

// parse returns done=true when the command should exit with code.
func (r *runner) parse(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(r.stdout, commandHelp[fs.Name()])
		return ExitOK, true
	case err != nil:
		fmt.Fprintf(r.stderr, "Run 'relaybox help %s' for usage.\n", fs.Name())
		return ExitUsage, true
	case fs.NArg() > 0:
		r.errorf("%s does not take arguments (got %q)", fs.Name(), fs.Arg(0))
		return ExitUsage, true
	}
	return 0, false
}

// config loads the configuration with flag values taking precedence over
// environment variables.
func (r *runner) config(overrides map[string]string) (config.Config, []string, int) {
	env := make(map[string]string, len(r.env)+len(overrides))
	for k, v := range r.env {
		env[k] = v
	}
	for k, v := range overrides {
		if v != "" {
			env[k] = v
		}
	}
	cfg, warnings, err := config.Load(env)
	if err != nil {
		fmt.Fprintln(r.stderr, "relaybox: invalid configuration:")
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintln(r.stderr, "  "+line)
		}
		return cfg, warnings, ExitUsage
	}
	return cfg, warnings, ExitOK
}

func (r *runner) serve(args []string) int {
	fs := r.flags("serve")
	host := fs.String("host", "", "")
	port := fs.String("port", "", "")
	dbPath := fs.String("database", "", "")
	publicURL := fs.String("public-url", "", "")
	if code, done := r.parse(fs, args); done {
		return code
	}
	cfg, warnings, code := r.config(map[string]string{
		config.EnvHost: *host, config.EnvPort: *port,
		config.EnvDatabasePath: *dbPath, config.EnvPublicBaseURL: *publicURL,
	})
	if code != ExitOK {
		return code
	}
	log := logging.New(r.stderr, cfg.LogLevel, cfg.LogFormat)
	for _, w := range warnings {
		log.Warn(w)
	}
	if err := server.Run(r.ctx, cfg, log); err != nil {
		log.Error(err.Error())
		return ExitError
	}
	return ExitOK
}

// openExisting opens the configured database, refusing to create a new one.
func (r *runner) openExisting(cfg config.Config) (*storage.DB, *settings.Service, int) {
	if _, err := os.Stat(cfg.DatabasePath); err != nil {
		r.errorf("no database at %s (set RELAYBOX_DATABASE_PATH or --database)", cfg.DatabasePath)
		return nil, nil, ExitError
	}
	db, err := storage.Open(r.ctx, cfg.DatabasePath)
	if err != nil {
		r.errorf("%v", err)
		return nil, nil, ExitError
	}
	set, warnings, err := settings.New(r.ctx, db, settings.DefaultsFromConfig(cfg), cfg.MaxBodyBytes)
	if err != nil {
		db.Close()
		r.errorf("load settings: %v", err)
		return nil, nil, ExitError
	}
	for _, w := range warnings {
		r.errorf("warning: %s", w)
	}
	return db, set, ExitOK
}

func (r *runner) cleanup(args []string) int {
	fs := r.flags("cleanup")
	dryRun := fs.Bool("dry-run", false, "")
	dbPath := fs.String("database", "", "")
	if code, done := r.parse(fs, args); done {
		return code
	}
	cfg, _, code := r.config(map[string]string{config.EnvDatabasePath: *dbPath})
	if code != ExitOK {
		return code
	}
	db, set, code := r.openExisting(cfg)
	if code != ExitOK {
		return code
	}
	defer db.Close()

	cur := set.Current()
	if cur.RetentionDays == 0 && cur.MaxStoredRequests == 0 {
		fmt.Fprintln(r.stdout, "Retention is off (requests are kept forever with no limit). Nothing to delete.")
		return ExitOK
	}
	fmt.Fprintf(r.stdout, "Policy: %s.\n", describePolicy(cur.Values))
	runner := retention.New(db, set, nil, nil)
	var res retention.Result
	var err error
	if *dryRun {
		res, err = runner.Plan(r.ctx)
	} else {
		res, err = runner.RunOnce(r.ctx)
	}
	if err != nil {
		r.errorf("cleanup failed: %v", err)
		return ExitError
	}
	total := res.Expired + res.Trimmed
	verb := "Deleted"
	if *dryRun {
		verb = "Would delete"
	}
	if total == 0 {
		fmt.Fprintln(r.stdout, "Nothing to delete.")
		return ExitOK
	}
	fmt.Fprintf(r.stdout, "%s %d %s (%d expired, %d over the limit).\n", verb, total, plural(total, "request", "requests"), res.Expired, res.Trimmed)
	return ExitOK
}

func describePolicy(v settings.Values) string {
	var parts []string
	if v.RetentionDays > 0 {
		parts = append(parts, fmt.Sprintf("keep requests for %d %s", v.RetentionDays, plural(int64(v.RetentionDays), "day", "days")))
	} else {
		parts = append(parts, "keep requests forever")
	}
	if v.MaxStoredRequests > 0 {
		parts = append(parts, fmt.Sprintf("store at most %d", v.MaxStoredRequests))
	} else {
		parts = append(parts, "no request limit")
	}
	return strings.Join(parts, ", ")
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (r *runner) export(args []string) int {
	fs := r.flags("export")
	endpoint := fs.String("endpoint", "", "")
	limit := fs.Int("limit", 100, "")
	out := fs.String("out", "", "")
	noBodies := fs.Bool("no-bodies", false, "")
	dbPath := fs.String("database", "", "")
	if code, done := r.parse(fs, args); done {
		return code
	}
	if *limit < 1 || *limit > fixture.MaxRequests {
		r.errorf("--limit must be between 1 and %d", fixture.MaxRequests)
		return ExitUsage
	}
	cfg, _, code := r.config(map[string]string{config.EnvDatabasePath: *dbPath})
	if code != ExitOK {
		return code
	}
	db, set, code := r.openExisting(cfg)
	if code != ExitOK {
		return code
	}
	defer db.Close()

	var endpointID int64
	if *endpoint != "" {
		ep, err := findEndpoint(r.ctx, db, *endpoint)
		if err != nil {
			r.errorf("%v", err)
			return ExitError
		}
		endpointID = ep.ID
	}
	ids, err := db.LatestRequestIDs(r.ctx, endpointID, *limit)
	if err != nil {
		r.errorf("%v", err)
		return ExitError
	}
	if len(ids) == 0 {
		r.errorf("there are no captured requests to export")
		return ExitError
	}
	reqs, err := db.GetRequests(r.ctx, ids)
	if err != nil {
		r.errorf("%v", err)
		return ExitError
	}
	f := fixture.Build(reqs, set.Current().Policy, fixture.BuildOptions{
		IncludeBodies: !*noBodies,
		Generator:     "relaybox " + version.Version,
		Now:           time.Now(),
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		r.errorf("%v", err)
		return ExitError
	}
	data = append(data, '\n')
	if *out == "" || *out == "-" {
		_, _ = r.stdout.Write(data)
		return ExitOK
	}
	if err := os.WriteFile(*out, data, 0o600); err != nil {
		r.errorf("write %s: %v", *out, err)
		return ExitError
	}
	fmt.Fprintf(r.stderr, "Exported %d %s to %s\n", len(reqs), plural(int64(len(reqs)), "request", "requests"), *out)
	return ExitOK
}

func findEndpoint(ctx context.Context, db *storage.DB, ref string) (storage.Endpoint, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil && id > 0 {
		ep, err := db.GetEndpoint(ctx, id)
		if err == nil {
			return ep, nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return ep, err
		}
	}
	eps, err := db.ListEndpoints(ctx)
	if err != nil {
		return storage.Endpoint{}, err
	}
	for _, ep := range eps {
		if strings.EqualFold(ep.Name, ref) {
			return ep, nil
		}
	}
	return storage.Endpoint{}, fmt.Errorf("no endpoint with the name or ID %q", ref)
}

func (r *runner) version(args []string) int {
	if len(args) > 0 && args[0] != "-h" && args[0] != "--help" {
		r.errorf("version does not take arguments")
		return ExitUsage
	}
	fmt.Fprintln(r.stdout, version.String())
	return ExitOK
}

func (r *runner) help(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(r.stdout, usage)
		return ExitOK
	}
	text, ok := commandHelp[args[0]]
	if !ok {
		r.errorf("unknown command %q", args[0])
		return ExitUsage
	}
	fmt.Fprint(r.stdout, text)
	return ExitOK
}

// healthcheck asks a running server for /healthz. It exists for container
// health checks in images that have no curl or wget.
func (r *runner) healthcheck(args []string) int {
	fs := r.flags("healthcheck")
	target := fs.String("url", "", "")
	if code, done := r.parse(fs, args); done {
		return code
	}
	if *target == "" {
		cfg, _, code := r.config(nil)
		if code != ExitOK {
			return code
		}
		*target = "http://" + net.JoinHostPort(connectHost(cfg.Host), strconv.Itoa(cfg.Port)) + "/healthz"
	}
	if err := checkHealth(r.ctx, *target); err != nil {
		r.errorf("health check failed: %v", err)
		return ExitError
	}
	return ExitOK
}

func checkHealth(ctx context.Context, target string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body) != nil || body.Status != "ok" {
		return fmt.Errorf("%s answered %s", target, resp.Status)
	}
	return nil
}

// connectHost returns an address for reaching a server listening on host.
func connectHost(host string) string {
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsUnspecified() {
		if addr.Is6() && !addr.Is4In6() {
			return "::1"
		}
		return "127.0.0.1"
	}
	return host
}

func (r *runner) doctor(args []string) int {
	fs := r.flags("doctor")
	host := fs.String("host", "", "")
	port := fs.String("port", "", "")
	dbPath := fs.String("database", "", "")
	if code, done := r.parse(fs, args); done {
		return code
	}
	cfg, warnings, code := r.config(map[string]string{config.EnvHost: *host, config.EnvPort: *port, config.EnvDatabasePath: *dbPath})
	if code != ExitOK {
		return code
	}
	failed := false
	report := func(status, format string, args ...any) {
		if status == "fail" {
			failed = true
		}
		fmt.Fprintf(r.stdout, "%-5s %s\n", status, fmt.Sprintf(format, args...))
	}

	report("ok", "%s", version.String())
	report("ok", "configuration is valid")
	for _, w := range warnings {
		report("warn", "%s", w)
	}

	absDB, err := filepath.Abs(cfg.DatabasePath)
	if err != nil {
		absDB = cfg.DatabasePath
	}
	if _, err := os.Stat(cfg.DatabasePath); errors.Is(err, os.ErrNotExist) {
		report("ok", "database %s does not exist yet; it will be created on first start", absDB)
	} else if err != nil {
		report("fail", "database %s: %v", absDB, err)
	} else if db, err := storage.Open(r.ctx, cfg.DatabasePath); err != nil {
		report("fail", "database %s: %v", absDB, err)
	} else {
		schema, _ := db.SchemaVersion(r.ctx)
		count, cerr := db.CountRequests(r.ctx)
		if err := db.QuickCheck(r.ctx); err != nil {
			report("fail", "database %s: %v", absDB, err)
		} else if cerr != nil {
			report("fail", "database %s: %v", absDB, cerr)
		} else {
			report("ok", "database %s (schema version %d, %s, %d stored %s, integrity ok)",
				absDB, schema, formatBytes(db.SizeBytes()), count, plural(count, "request", "requests"))
		}
		db.Close()
	}

	if webui.Dist() != nil {
		report("ok", "dashboard files are included in this binary")
	} else {
		report("warn", "this binary was built without the dashboard; run make build")
	}

	addr := cfg.Addr()
	if ln, err := net.Listen("tcp", addr); err == nil {
		ln.Close()
		report("ok", "%s is free", addr)
	} else if checkHealth(r.ctx, "http://"+net.JoinHostPort(connectHost(cfg.Host), strconv.Itoa(cfg.Port))+"/healthz") == nil {
		report("ok", "a Relaybox server is already running on %s", addr)
	} else {
		report("fail", "cannot listen on %s: %v", addr, err)
	}

	if cfg.AllowRemoteDashboard {
		report("info", "dashboard and API accept remote clients that present the dashboard token")
	} else {
		report("info", "dashboard and API only accept clients on this machine")
	}
	report("info", "capture URLs start with %s/hooks/; anyone who has one can send requests to it", cfg.PublicBaseURL)
	if cfg.URLReplayEnabled() {
		report("info", "replay to public URLs is available (private and internal addresses are blocked)")
	} else {
		report("info", "replay to URLs is disabled; replay into endpoints still works")
	}
	if len(cfg.ReplayAllowedHosts) > 0 {
		report("warn", "replay may reach these private hosts: %s", strings.Join(cfg.ReplayAllowedHosts, ", "))
	}
	if failed {
		return ExitError
	}
	return ExitOK
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
