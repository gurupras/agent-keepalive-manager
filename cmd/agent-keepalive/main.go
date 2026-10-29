// Command agent-keepalive is the local server and its command line: it
// keeps the provider prompt cache of chosen Claude Code and Codex CLI
// sessions warm, and serves a web UI to choose them.
//
//	agent-keepalive serve        run the server in the foreground
//	agent-keepalive app          start the server in the background if needed and open the desktop window
//	agent-keepalive open         start the server in the background if needed and open the UI in a browser
//	agent-keepalive status       is a server running, and where
//	agent-keepalive stop         stop the running server
//	agent-keepalive metrics on|off|status|preview|reset-id
//	agent-keepalive update check|apply|status
//	agent-keepalive version
//
// With no arguments, the installs that carry the desktop window (the Linux
// packages, AppImage, Windows setup and portable, macOS pkg) run `app`, or
// `serve` on a Linux machine with no display; the archives, Homebrew and
// dev builds run `serve`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gurupras/agent-keepalive-manager/internal/app"
	"github.com/gurupras/agent-keepalive-manager/internal/distro"
	"github.com/gurupras/agent-keepalive-manager/internal/instance"
	"github.com/gurupras/agent-keepalive-manager/internal/update"
	"github.com/gurupras/agent-keepalive-manager/web"
)

// EnvFrameAncestors is the environment form of --frame-ancestors.
const EnvFrameAncestors = "AGENT_KEEPALIVE_FRAME_ANCESTORS"

// version is set at link time: -ldflags "-X main.version=1.4.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// errUsage is returned for bad arguments; the usage was already printed.
var errUsage = errors.New("usage")

func run(args []string, stdout, stderr io.Writer) int {
	cmd, rest := "", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, rest = args[0], args[1:]
	}
	if cmd == "" {
		cmd = defaultCommand(app.InstallKind())
		if cmd == "app" && !hasDisplay() {
			cmd = "serve"
		}
	}
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(rest, stderr)
	case "app":
		err = cmdApp(rest, stdout, stderr)
	case "open":
		err = cmdOpen(rest, stdout, stderr)
	case "status":
		err = cmdStatus(rest, stdout)
	case "stop":
		err = cmdStop(rest, stdout)
	case "metrics":
		err = cmdMetrics(rest, stdout)
	case "update":
		err = cmdUpdate(rest, stdout)
	case "version", "--version", "-version":
		cmdVersion(stdout)
	case "help", "-h", "--help", "-help":
		usage(stdout)
	default:
		fmt.Fprintf(stderr, "agent-keepalive: unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
	var nr *notRunningError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage), errors.Is(err, flag.ErrHelp):
		return 2
	case errors.As(err, &nr):
		fmt.Fprintln(stderr, "agent-keepalive:", err)
		return 3
	default:
		fmt.Fprintln(stderr, "agent-keepalive:", err)
		return 1
	}
}

// defaultCommand is what running the program without arguments does:
// installs that carry the desktop window open it.
func defaultCommand(kind string) string {
	switch kind {
	case update.KindDeb, update.KindRPM, update.KindArch, update.KindAppImage,
		update.KindWindowsPortable, update.KindWindowsSetup, update.KindMacOSPkg:
		return "app"
	}
	return "serve"
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: agent-keepalive <command> [flags]

Commands:
  app        start the server in the background if needed and open the desktop window
             (the default for the desktop installs)
  serve      run the server in the foreground (the default otherwise)
  open       start the server in the background if needed and open the UI in a browser
  status     show whether a server is running
  stop       stop the running server
  metrics    on | off | status | preview | reset-id   (usage reports)
  update     check | apply | status                   (self-update)
  version    print the version, distribution profile and install kind

Run 'agent-keepalive <command> -h' for a command's flags.
`)
}

func cmdVersion(w io.Writer) {
	p := distro.Current()
	fmt.Fprintf(w, "agent-keepalive %s\nprofile: %s\ninstall: %s\n", version, p.Name, app.InstallKind())
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("agent-keepalive "+name, flag.ContinueOnError)
	return fs
}

// dataDirFlag adds --data-dir and returns a resolver for it.
func dataDirFlag(fs *flag.FlagSet) func() (string, error) {
	d := fs.String("data-dir", "", "state directory (default: $"+instance.EnvDataDir+", else the per-user default)")
	return func() (string, error) { return instance.DataDir(*d, app.InstallKind()) }
}

func cmdServe(args []string, stderr io.Writer) error {
	fs := newFlags("serve")
	fs.SetOutput(stderr)
	dataDir := dataDirFlag(fs)
	port := fs.Int("port", 0, "listen on exactly this port on 127.0.0.1 (default: $PORT, else 4344 or the next free one)")
	pricingPath := fs.String("pricing", "", "pricing.json override (default <data dir>/pricing.json)")
	logLevel := fs.String("log-level", "info", "debug, info, warn or error")
	addon := fs.String("ui-addon", "", "directory served under addon/; its addon.css and addon.js are linked into the UI")
	frames := fs.String("frame-ancestors", os.Getenv(EnvFrameAncestors),
		"pages allowed to frame the UI, as CSP frame-ancestors sources (default 'self'; $"+EnvFrameAncestors+")")
	stdinEOF := fs.Bool("exit-on-stdin-eof", false, "stop when standard input closes (for a supervising program)")
	noUpdate := fs.Bool("no-update", false, "turn self-update off")
	supervised := fs.Bool("supervised", false, "run under another program: no self-update, no quit from the UI")
	if err := fs.Parse(args); err != nil {
		return errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "serve: unexpected argument %q\n", fs.Arg(0))
		return errUsage
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		fmt.Fprintf(stderr, "serve: bad --log-level %q\n", *logLevel)
		return errUsage
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if *port == 0 {
		if *port, err = instance.PortFromEnv(); err != nil {
			return err
		}
	}

	// A server started by a self-update has no terminal: it logs to the
	// data directory, like one started by `open`.
	logOut := stderr
	_, relaunched := update.RelaunchedBy()
	if relaunched {
		if f, err := openLog(dir); err == nil {
			defer f.Close()
			logOut = f
		}
	}
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *stdinEOF {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			log.Info("standard input closed; stopping")
			stop()
		}()
	}

	cfg := app.Config{
		Version: version, DataDir: dir, Port: *port, PricingPath: *pricingPath,
		UI: web.UI(), UIAddon: *addon, FrameAncestors: *frames, Supervised: *supervised, NoUpdate: *noUpdate,
		InstallKind: app.InstallKind(), Log: log,
	}
	if relaunched {
		cfg.WaitPredecessor = app.WaitForPredecessor
	}
	err = app.Serve(ctx, cfg)
	var re *app.RestartError
	if errors.As(err, &re) {
		log.Info("restarting into the update", "exe", re.Exe)
		return update.Relaunch(re.Exe, os.Args[1:])
	}
	return err
}
