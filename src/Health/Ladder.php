<?php

namespace ErnestDefoe\Garrison\Health;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Model\Incident;
use ErnestDefoe\Garrison\Model\Server;
use ErnestDefoe\Garrison\Notification\Alerts;
use Flarum\User\User;

/**
 * The remediation ladder: decide whether to act on an unhealthy server, and
 * stop deciding once acting has clearly not worked.
 *
 * 🚨 This class is the one that can do real damage, and every rule in it is
 * there to stop Garrison becoming the most common cause of the outages it
 * exists to prevent.
 *
 *   1. Act on a RUN, never one reading. A single unready poll is a network
 *      hiccup; three consecutive ones is a server.
 *   2. Restart once, then wait and look again. A second restart before the
 *      first has had time to take effect is a loop.
 *   3. FLAP DETECTION. Three restarts inside ten minutes means restarting is
 *      not the answer — stop, mark it, and tell a human. A server that is
 *      honestly down and says so is far better than one being kicked every
 *      thirty seconds while nobody understands why.
 *   4. Never touch a server whose operator turned this off, and never touch
 *      one already marked as needing attention.
 *
 * It runs on the FORUM rather than the agent on purpose: the forum has the
 * history, the audit log and the schedule, and keeping the decision here means
 * the agent stays a dumb executor of a fixed verb set.
 */
class Ladder
{
    /** Consecutive unready polls before the ladder will act. */
    public const RUN_BEFORE_ACTING = 3;

    /** A restart is given this long to take effect before anything else. */
    public const RESTART_GRACE_SECONDS = 180;

    /** More than this many restarts inside FLAP_WINDOW means stop trying. */
    public const FLAP_RESTARTS = 3;
    public const FLAP_WINDOW_SECONDS = 600;

    public function __construct(
        protected Dispatcher $dispatcher,
        protected Alerts $alerts
    ) {
    }

    /**
     * Fold one health report into a server's state, and act if the rules say
     * to. Called once per server per scheduler tick.
     *
     * @return string|null what was done, for logging. Null means nothing.
     */
    public function evaluate(Server $server, User $actor): ?string
    {
        /**
         * 🚨 NULL IS NOT UNHEALTHY. This very nearly restarted a live Valheim
         * server with players on it.
         *
         * A server whose agent has not reported health yet — an older agent, a
         * game with no probes, the first minutes after an upgrade — has
         * `health_state` NULL. The first version of this method treated
         * anything that was not 'ok' or 'unknown' as a fault, so three ticks
         * later it queued a restart for every such server. On a fresh upgrade
         * that is EVERY server on the forum, restarted at once, because
         * Garrison had not heard from them yet.
         *
         * "We have not looked" and "we looked and it is broken" must never be
         * the same branch. The agent already encodes that rule; the ladder has
         * to as well, because it is the half that acts.
         */
        if ($server->health_state === null || $server->health_state === '') {
            return null;
        }

        $healthy = in_array($server->health_state, ['ok', 'unknown'], true);

        if ($healthy) {
            return $this->recover($server);
        }

        // 'down' counts as unhealthy, but a server an operator deliberately
        // stopped is not an incident. Only a server that was RUNNING and has
        // stopped answering is.
        if ($server->health_state === 'down' && $server->state === 'stopped') {
            return $this->recover($server);
        }

        $server->unready_polls = (int) $server->unready_polls + 1;

        if ($server->unready_since === null) {
            $server->unready_since = Carbon::now();
        }

        $server->save();

        if ($server->unready_polls < self::RUN_BEFORE_ACTING) {
            return null; // still might be a hiccup
        }

        return $this->act($server, $actor);
    }

    /**
     * A server that has come back: clear the counters and close its incident.
     */
    protected function recover(Server $server): ?string
    {
        if ($server->unready_polls === 0 && $server->unready_since === null) {
            return null;
        }

        $server->unready_polls = 0;
        $server->unready_since = null;

        // 🚨 needs_attention is NOT cleared automatically. The ladder gave up
        // because restarting did not help; a server that happens to look fine
        // one poll later has not proved the underlying fault is gone, and
        // silently re-arming would hide a recurring problem. A person clears
        // it, having looked.
        $server->save();

        $incident = Incident::query()
            ->where('server_id', $server->id)
            ->where('status', 'open')
            ->latest('id')
            ->first();

        if ($incident !== null) {
            $incident->status = 'resolved';
            $incident->resolved_at = Carbon::now();
            $incident->appendAction('recovered — health is ' . $server->health_state);
            $incident->save();

            // 🚨 Told about the recovery as well as the fault. An alert that
            // only ever says "it broke" trains people to ignore it, because
            // they have no way to know from the alerts alone whether anything
            // is currently wrong.
            $this->alerts->serverIncident($server, 'recovered');

            return 'resolved incident ' . $incident->id;
        }

        return null;
    }

    protected function act(Server $server, User $actor): ?string
    {
        /**
         * 🚨 Checked BEFORE opening an incident, and the order is the bug.
         *
         * With the check after, a server the ladder had already given up on
         * opened a FRESH incident on the very next tick — then returned
         * without acting. The incident list would fill with one empty record
         * per minute for a server nobody had got round to fixing yet, burying
         * the real one that says what was tried.
         */
        if ($server->needs_attention) {
            return null; // already given up; say nothing more
        }

        $incident = $this->openIncident($server);

        if (! $server->auto_remediate) {
            $incident->appendActionOnce('auto-remediation is off for this server — not acting');
            $incident->save();

            return null;
        }

        // Has a restart already been tried and not yet had its chance?
        if ($server->last_remediation_at !== null
            && $server->last_remediation_at->gt(Carbon::now()->subSeconds(self::RESTART_GRACE_SECONDS))) {
            return null;
        }

        // 🚨 Flapping. Counted on the incident rather than globally, so a
        // server that broke, was fixed, and broke again next week starts
        // fresh — while one being kicked every few minutes does not.
        if ($incident->restarts >= self::FLAP_RESTARTS
            && $incident->started_at->gt(Carbon::now()->subSeconds(self::FLAP_WINDOW_SECONDS * 2))) {
            $server->needs_attention = true;
            $server->save();

            $incident->status = 'abandoned';
            $incident->appendAction(sprintf(
                'gave up after %d restarts — restarting is not fixing this, a person needs to look',
                $incident->restarts
            ));
            $incident->save();

            // The one alert that must not be missed: automatic recovery has
            // stopped, and nothing else will happen until somebody acts.
            $this->alerts->serverIncident($server, 'abandoned', $server->health_summary);

            return 'abandoned incident ' . $incident->id;
        }

        $this->dispatcher->queue($actor, $server, 'server.restart', [], 'health');

        $incident->restarts = (int) $incident->restarts + 1;
        $incident->appendAction(sprintf('restart #%d queued (%s)', $incident->restarts, $server->health_summary ?: 'unready'));
        $incident->save();

        $server->last_remediation_at = Carbon::now();
        $server->save();

        return 'restarted ' . $server->ref;
    }

    protected function openIncident(Server $server): Incident
    {
        $incident = Incident::query()
            ->where('server_id', $server->id)
            ->where('status', 'open')
            ->latest('id')
            ->first();

        if ($incident !== null) {
            return $incident;
        }

        // 🚨 Alerted when the incident OPENS, not when the ladder gives up.
        // By the time it has given up the server has been unreachable for
        // several minutes; the point of telling somebody is that they might
        // get there first.
        $this->alerts->serverIncident(
            $server,
            $server->health_state === 'down' ? 'down' : 'unready',
            $server->health_summary
        );

        $incident = new Incident();
        $incident->server_id = $server->id;
        $incident->started_at = $server->unready_since ?? Carbon::now();
        $incident->cause = $server->health_summary ?: ('health is ' . $server->health_state);
        $incident->detail = $server->health_checks;
        $incident->status = 'open';
        $incident->restarts = 0;
        $incident->save();

        return $incident;
    }
}
