// Command garrison-agent runs game servers on one host and answers the forum.
//
// It dials OUT to the forum and holds that connection open, so this host needs
// no inbound port, no port forward and no static IP.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ernestdefoe/garrison/internal/agent"
	"github.com/ernestdefoe/garrison/internal/config"
	"github.com/ernestdefoe/garrison/internal/driver"
)

func main() {
	var (
		cfgPath = flag.String("config", "/etc/garrison/agent.json", "path to the agent config file")
		verbose = flag.Bool("v", false, "debug logging")
		check   = flag.Bool("check", false, "validate the config and report what this host can do, then exit")
	)
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	processDriver := driver.NewProcess()
	candidates := driver.Set{
		"docker":  driver.NewDocker(),
		"process": processDriver,
	}

	ag, unavailable := agent.New(ctx, cfg.Servers, candidates)

	/*
	 * 🚨 Provisioning is wired HERE, from a config that came from a file — so
	 * `register` writes back to that same file and a provisioned server
	 * survives a restart.
	 *
	 * An agent built any other way gets no templates and no register function,
	 * which means provision.install simply reports that it cannot persist
	 * anything. That is the right default: the ability to add servers should
	 * follow from an operator having written a config, not from the code
	 * happening to be running.
	 */
	ag.Provisioning(cfg.Templates, cfg.Add)
	for _, u := range unavailable {
		// Not an error. A host with no Docker is a host Garrison supports; it
		// simply cannot run the servers configured for that driver, and the
		// operator should hear it once at startup rather than at the first
		// click.
		log.Info("driver unavailable on this host", "driver", u)
	}

	if *check {
		/*
		 * 🚨 Exits NON-ZERO when something is wrong, so this can be the last
		 * line of an install script or a CI step. A check that always succeeds
		 * is a check nobody wires into anything, and then it only runs when
		 * somebody already suspects a problem — which is far too late for the
		 * things it catches.
		 */
		if !report(ctx, cfg.Path(), cfg.Servers, ag) {
			os.Exit(1)
		}

		return
	}

	// 🚨 Stopping the agent must not stop the games. Detaching rather than
	// signalling is what makes an agent upgrade a non-event instead of an
	// outage — and an agent that takes the servers down when it restarts is
	// an agent nobody will let auto-update.
	defer processDriver.Shutdown()

	// The scheme picks the transport. Polling is the default because it needs
	// nothing on the forum host but Flarum itself; websocket needs a gateway
	// daemon there and exists as an upgrade, not a requirement.
	var run func(context.Context) error

	switch {
	case strings.HasPrefix(cfg.ForumURL, "ws://"), strings.HasPrefix(cfg.ForumURL, "wss://"):
		run = agent.NewLink(cfg.ForumURL, cfg.Token, ag, log).Run
		log.Info("transport", "kind", "websocket")
	default:
		run = agent.NewHTTPLink(cfg.ForumURL, cfg.Token, ag, log).Run
		log.Info("transport", "kind", "poll")
	}

	log.Info("starting", "version", agent.Version, "forum", cfg.ForumURL, "servers", len(cfg.Servers))

	if err := run(ctx); err != nil && ctx.Err() == nil {
		log.Error("link", "err", err)
		os.Exit(1)
	}
	log.Info("stopped")
}

/*
report prints the preflight findings, grouped by server, and says whether the
configuration is usable.

🚨 Grouped and printed in full rather than stopping at the first fault. An
operator fixing a hand-written JSON file wants the list — stopping at the first
problem turns one editing session into a game of whack-a-mole with an agent
restart between each round.
*/
func report(ctx context.Context, configPath string, servers []driver.Server, ag *agent.Agent) bool {
	findings := agent.Check(ctx, configPath, servers, ag.Drivers(), ag.Unavailable())

	byServer := map[string][]agent.Finding{}
	var order []string

	for _, f := range findings {
		if _, seen := byServer[f.Server]; !seen {
			order = append(order, f.Server)
		}

		byServer[f.Server] = append(byServer[f.Server], f)
	}

	bad := 0

	for _, name := range order {
		if name != "" {
			fmt.Printf("\n%s\n", name)
		}

		for _, f := range byServer[name] {
			mark := "  ok  "
			if f.Bad {
				mark = "  BAD "
				bad++
			}

			fmt.Printf("%s %s\n", mark, f.Text)
		}
	}

	fmt.Printf("\n%d server(s) configured, %d problem(s)\n", len(servers), bad)

	return bad == 0
}
