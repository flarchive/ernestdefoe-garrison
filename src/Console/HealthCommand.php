<?php

namespace ErnestDefoe\Garrison\Console;

use ErnestDefoe\Garrison\Agent\Gateway;
use ErnestDefoe\Garrison\Game\Artwork;
use ErnestDefoe\Garrison\Health\Ladder;
use ErnestDefoe\Garrison\Model\Server;
use ErnestDefoe\Garrison\Players\Tracker;
use ErnestDefoe\Garrison\Health\Heartbeat;
use Flarum\Console\AbstractCommand;
use Flarum\User\User;

/**
 * Runs the remediation ladder over every server.
 *
 * 🚨 Signature is `fire(): int` — Flarum's AbstractCommand declares it
 * abstract with that return type, and `: void` is a fatal at class load that
 * kills the ENTIRE console with exit 255 and no output, while the web keeps
 * serving 200. Learned the hard way on this extension's first install.
 */
class HealthCommand extends AbstractCommand
{
    public function __construct(
        protected Ladder $ladder,
        protected Artwork $artwork,
        protected Gateway $gateway,
        protected Heartbeat $heartbeat,
        protected Tracker $tracker
    ) {
        parent::__construct();
    }

    protected function configure(): void
    {
        $this
            ->setName('garrison:health')
            ->setDescription('Check reported server health and act on it');
    }

    protected function fire(): int
    {
        /**
         * 🚨 The ladder's commands are attributed to the actor that owns the
         * forum, and marked source=health in the audit log. An automatic
         * restart must be as traceable as a human one — "who restarted my
         * server at 3am" has to have an answer, and "nobody, the system did,
         * here is why" is that answer.
         */
        $actor = User::query()->where('id', 1)->first();

        if ($actor === null) {
            $this->error('No actor available to attribute health actions to.');

            return static::FAILURE;
        }

        $acted = 0;

        $closed = 0;

        Server::query()->each(function (Server $server) use ($actor, &$acted, &$closed) {
            $what = $this->ladder->evaluate($server, $actor);

            if ($what !== null) {
                $this->info($server->ref . ': ' . $what);
                $acted++;
            }

            /*
             * 🚨 A silent agent ends everybody's session.
             *
             * The agent closes sessions itself when a server STOPS, because it
             * can see that happen. What it cannot report is its own death — a
             * host that loses power, a container that is killed, a network that
             * goes away — and without this, every session open at that moment
             * runs until somebody notices. Every hour of that outage becomes
             * playtime for whoever happened to be on when it fell over, which
             * is a leaderboard that rewards being online during a failure.
             *
             * Staleness is already the forum's word for "this is a memory, not
             * a fact", and it is the right threshold here for the same reason.
             */
            if ($server->isStale()) {
                $closed += $this->tracker->closeAll($server);
            }
        });

        if ($closed > 0) {
            $this->info('closed ' . $closed . ' play session(s) on servers that stopped reporting');
        }

        /**
         * 🚨 Artwork rides on the same tick rather than having its own
         * schedule. A server that reports a known game gets its logo without
         * anybody finding a button — which is how every other game panel
         * behaves, and what an operator expects. Bounded by icon_attempts so a
         * game whose artwork 404s is not fetched every minute for ever.
         */
        $got = $this->artwork->backfill();

        if ($got > 0) {
            $this->info('fetched artwork for ' . $got . ' server(s)');
        }

        /**
         * 🚨 Pruned here rather than never. The console table grows forever
         * otherwise — a busy server at a few hundred lines a minute is tens of
         * millions of rows a year, which eventually makes the FORUM's own
         * backups fail, for output nobody will ever read.
         *
         * Once an hour is plenty, and cheap to decide: the tick runs every
         * minute, so this is the minute-zero one.
         */
        if ((int) date('i') === 0) {
            $pruned = $this->gateway->pruneConsole();

            if ($pruned > 0) {
                $this->info('pruned ' . $pruned . ' old console line(s)');
            }
        }

        /**
        /**
         * 🚨 Garrison's own machinery proving itself, on the same tick.
         *
         * Two things this product completely depends on and does not own: the
         * SCHEDULER that runs this very command, and the QUEUE WORKER that
         * delivers every alert. Neither announces its absence — a forum with no
         * cron entry runs none of this and looks entirely normal, and a forum
         * with no worker writes no notifications while `sync()` returns
         * happily.
         *
         * Stamping a time here proves the first (this line only runs if the
         * scheduler did), and the job it pushes proves the second when a worker
         * runs it. See Heartbeat — it exists because the queue was dead for
         * three days on the forum this extension was built on, and the only
         * reason it was found was somebody going looking.
         */
        $this->heartbeat->beat();

        if ($acted === 0 && $got === 0 && $closed === 0) {
            $this->info('Nothing to do.');
        }

        return static::SUCCESS;
    }
}
