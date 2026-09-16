<?php

namespace ErnestDefoe\Garrison\Agent;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Model\Command;
use ErnestDefoe\Garrison\Model\GarrisonAgent;
use ErnestDefoe\Garrison\Model\Server;
use ErnestDefoe\Garrison\Players\Tracker;
use Illuminate\Database\ConnectionInterface;

/**
 * The forum half of the agent link.
 *
 * 🚨 THE TRANSPORT DECISION, and everything about installing Garrison follows
 * from it.
 *
 * The agent holds a long-lived connection, which PHP cannot do — a request
 * handler is not a daemon. The tempting answer is to ship a small Go gateway
 * that runs on the forum host alongside Flarum and holds the sockets. It would
 * be technically nicer and it is the wrong trade: it means Garrison cannot be
 * installed by `composer require`, it will not run on shared hosting at all,
 * and every support thread starts with "is the gateway running?".
 *
 * So the agent LONG-POLLS over ordinary HTTPS instead. It asks for work, the
 * forum holds the request open for a few seconds, and answers the moment
 * something is queued. Measured on the dev host: `max_execution_time=0` under
 * FPM and `fastcgi_read_timeout 900`, so holding a request for 25 seconds is
 * comfortable — and where a host will not allow it, the agent falls back to
 * short polling and the product still works, just less promptly.
 *
 * What this keeps: the agent still dials OUT, so a game host needs no inbound
 * port. What it costs: console output is near-real-time rather than instant.
 * A gateway can be added later as an OPTIONAL upgrade for people who want
 * sub-second streaming; it must never be the only way in.
 */
class Gateway
{
    /**
     * How long a poll may be held open. Shorter than any plausible proxy
     * timeout, so an idle poll always ends as a clean empty answer rather than
     * a 504 the agent has to interpret.
     */
    public const POLL_SECONDS = 25;

    /**
     * 🚨 Sleep between queue checks. Long enough that a parked agent is not a
     * database query every millisecond — with a dozen agents that is the whole
     * connection pool — and short enough that a button press feels immediate.
     */
    public const POLL_TICK_MS = 400;

    public function __construct(
        protected ConnectionInterface $db,
        protected Tracker $tracker
    ) {
    }

    /**
     * Hold the request until this agent has work, or the window closes.
     *
     * @return array<int, Command>
     */
    public function awaitCommands(GarrisonAgent $agent, int $seconds = self::POLL_SECONDS): array
    {
        $deadline = microtime(true) + $seconds;

        do {
            $commands = $this->takeQueued($agent);

            if ($commands !== []) {
                return $commands;
            }

            usleep(self::POLL_TICK_MS * 1000);
        } while (microtime(true) < $deadline);

        return [];
    }

    /**
     * Claim every queued command for this agent, marking them delivered.
     *
     * 🚨 Marked delivered in the SAME transaction that reads them. Two polls
     * can overlap — a slow network leaves the previous request still running
     * when the agent gives up and redials — and without this both would be
     * handed the same restart. Restarting a server twice is a visible fault;
     * doing it because of our own retry is an embarrassing one.
     *
     * @return array<int, Command>
     */
    public function takeQueued(GarrisonAgent $agent): array
    {
        return $this->db->transaction(function () use ($agent) {
            $commands = Command::query()
                ->where('agent_id', $agent->id)
                ->where('status', 'queued')
                ->orderBy('id')
                ->limit(50)
                ->lockForUpdate()
                ->get();

            if ($commands->isEmpty()) {
                return [];
            }

            Command::query()
                ->whereIn('id', $commands->pluck('id'))
                ->update(['status' => 'delivered', 'delivered_at' => Carbon::now()]);

            return $commands->all();
        });
    }

    /**
     * Record the agent's answer to one command.
     */
    public function recordResult(GarrisonAgent $agent, int $commandId, bool $ok, ?array $data, ?string $errorCode, ?string $errorMessage): void
    {
        /** @var Command|null $command */
        $command = Command::query()
            ->where('id', $commandId)
            ->where('agent_id', $agent->id) // an agent may only answer its own
            ->first();

        if ($command === null) {
            return;
        }

        // A result for a command already completed is a duplicate delivery,
        // not new information. Dropping it keeps the audit log honest about
        // when something actually happened.
        if (in_array($command->status, ['done', 'failed'], true)) {
            return;
        }

        $command->status = $ok ? 'done' : 'failed';
        $command->result = $data === null ? null : json_encode($data);
        $command->error_code = $errorCode;
        $command->error_message = $errorMessage;
        $command->completed_at = Carbon::now();
        $command->save();
    }

    /**
     * Fold a status report into the cached server row.
     *
     * 🚨 updateOrCreate keyed on (agent_id, ref) and NOT on anything the agent
     * chose freely. An agent cannot introduce a server under another agent's
     * id, and it cannot rename its way into a row that is not its own.
     */
    public function recordStatus(GarrisonAgent $agent, array $report): void
    {
        $ref = (string) ($report['server'] ?? '');

        if ($ref === '') {
            return;
        }

        /**
         * 🚨 Looked up and assigned field by field, never firstOrNew() with an
         * array. Flarum's AbstractModel guards mass assignment, so the array
         * form throws MassAssignmentException — but the better reason is that
         * this data comes from a REMOTE agent. Assigning explicitly means the
         * set of columns an agent can influence is visible in this method and
         * cannot widen by someone adding a field to the payload later.
         */
        $server = Server::query()
            ->where('agent_id', $agent->id)
            ->where('ref', $ref)
            ->first();

        if ($server === null) {
            $server = new Server();
            $server->agent_id = $agent->id;
            $server->ref = $ref;
            $server->name = (string) ($report['name'] ?? $ref);
            $server->created_at = Carbon::now();
        }

        $server->driver = (string) ($report['driver'] ?? $server->driver ?? 'unknown');

        // The agent knows what game it is running because its own config says
        // so. The forum never guesses from an image name — a guess that is
        // wrong once is worse than an honest "unknown".
        if (! empty($report['game'])) {
            $server->game = (string) $report['game'];
        }
        $server->state = (string) ($report['state'] ?? 'unknown');
        $server->state_detail = $report['detail'] ?? null;
        $server->pid = isset($report['pid']) ? (int) $report['pid'] : null;

        /**
         * 🚨 Guarded at the boundary as well as at the source. Go's zero time
         * marshals as "0001-01-01T00:00:00Z", which is non-empty and parses
         * cleanly — the status page cheerfully showed "Started Dec 31, 0000".
         * The agent no longer sends it, and this refuses it anyway: data from
         * a remote process is checked here, not trusted to have been checked
         * over there.
         */
        $server->running_since = null;

        if (! empty($report['since'])) {
            try {
                $since = Carbon::parse($report['since']);

                if ($since->year > 2000) {
                    $server->running_since = $since;
                }
            } catch (\Throwable) {
                // An unparseable timestamp is not worth failing a whole status
                // report over; the rest of it is still useful.
            }
        }

        if (isset($report['stats']) && is_array($report['stats'])) {
            $stats = $report['stats'];
            $server->cpu_percent = isset($stats['cpuPercent']) ? (float) $stats['cpuPercent'] : null;
            $server->memory_bytes = isset($stats['memoryBytes']) ? (int) $stats['memoryBytes'] : null;
            $server->memory_limit = ! empty($stats['memoryLimit']) ? (int) $stats['memoryLimit'] : null;
            // Kept because it is the difference between a number the forum can
            // present as exact and one it should label approximate: cgroup2
            // and docker are the kernel's own accounting, ps is an estimate.
            $server->stats_source = $stats['source'] ?? null;
        }

        /**
         * 🚨 Stored as the agent reported it, and NOT interpreted here.
         * Deciding what to do about it is the ladder's job, on a schedule,
         * with the history in front of it — a poll handler that also restarts
         * servers would act on one reading, which is the single worst thing
         * this feature could do.
         */
        if (isset($report['health']) && is_array($report['health'])) {
            $health = $report['health'];
            $server->health_state = (string) ($health['state'] ?? 'unknown');
            $server->health_summary = $health['summary'] ?? null;
            $server->health_checks = isset($health['results'])
                ? json_encode($health['results'])
                : null;
        }

        /**
         * 🚨 Validated here, not trusted. The agent generates these ids and
         * refuses anything that does not match the pattern it generates — but
         * this row is what the forum will later hand BACK as the id to restore
         * or delete, so the forum checks the shape too.
         *
         * Not because the agent's check is doubted: because the two checks
         * fail differently. A malformed id that gets stored here becomes a
         * button in somebody's browser that can only ever error, and the
         * operator reading "restore failed" has no way to tell a broken
         * archive from a broken panel. Refusing it at the door means the
         * button is never drawn.
         *
         * `is_array` on the whole field first: a report without backups (every
         * server that has none configured, which is most of them) must not
         * overwrite anything, and `[]` is a real answer meaning "none left"
         * that MUST overwrite — somebody just deleted the last one.
         */
        if (isset($report['backups']) && is_array($report['backups'])) {
            $server->backups = json_encode($this->validBackups($report['backups']));
        }

        /**
         * 🚨 An ABSENT `offsite` clears the flags; it does not leave them
         * standing.
         *
         * An operator who removes the bucket keys from the agent's config has
         * turned off-site copies OFF, and the panel must stop claiming
         * otherwise. Treating absence as "no news" would leave a server showing
         * "copies are going to b2" months after they stopped — the most
         * dangerous kind of stale, because it is a reassurance.
         */
        $offsite = $report['offsite'] ?? null;

        if (is_array($offsite) && ! empty($offsite['configured'])) {
            $server->offsite_configured = true;
            $server->offsite_bucket = isset($offsite['bucket']) ? (string) $offsite['bucket'] : null;
            $server->offsite_last_ok = ! empty($offsite['lastOk']);
            $server->offsite_last_error = empty($offsite['lastError']) ? null : (string) $offsite['lastError'];

            $server->offsite_last_at = null;

            if (! empty($offsite['lastAt'])) {
                try {
                    $at = Carbon::parse($offsite['lastAt']);

                    // The same year guard the running_since field needs: a Go
                    // zero time marshals as 0001-01-01 and PHP parses it
                    // happily, which once put "Dec 31, 0000" on a status page.
                    if ($at->year > 2000) {
                        $server->offsite_last_at = $at;
                    }
                } catch (\Throwable) {
                    // An unparseable timestamp is not worth failing a whole
                    // status report over.
                }
            }
        } else {
            $server->offsite_configured = false;
            $server->offsite_bucket = null;
            $server->offsite_last_at = null;
            $server->offsite_last_ok = false;
            $server->offsite_last_error = null;
        }

        /**
         * 🚨 Who is playing, and the difference between "nobody" and "this
         * server does not say".
         *
         * They look identical in an empty list and mean opposite things on a
         * panel: one is an empty game, the other is a feature nobody turned on.
         * Showing "0 players" for the second is a quiet lie that makes an
         * operator think their server is dead.
         */
        $known = ! empty($report['playersKnown']);
        $online = $known ? array_values((array) ($report['players'] ?? [])) : null;

        $server->players_known = $known;
        $server->players_online_names = $known ? json_encode($online) : null;

        $server->last_status_at = Carbon::now();
        $server->updated_at = Carbon::now();
        $server->save();

        /*
         * 🚨 Sessions are reconciled AFTER the save, and outside it.
         *
         * The tracker writes its own rows, and doing that before the server
         * row is saved would leave a session pointing at a server whose state
         * the forum has not yet recorded — the two would disagree for the
         * length of one request, which is exactly long enough for a page load
         * to catch it.
         */
        $this->tracker->sync($server, $online);
    }

    /**
     * 🚨 The same pattern the agent generates, repeated on this side.
     *
     * Kept in step with internal/backup/backup.go by intent rather than by
     * machinery — there is no way for PHP to import a Go constant. The pattern
     * is deliberately strict: it is what stands between a backup id and an
     * arbitrary path, on both halves of this product.
     */
    protected const BACKUP_ID = '/^[a-z0-9][a-z0-9_-]{0,63}-\d{8}-\d{6}-[a-z0-9]{4}(-safety)?\.tar\.gz$/';

    /**
     * @param array<int, mixed> $reported
     * @return array<int, array{id: string, size: int, at: string, safety: bool}>
     */
    protected function validBackups(array $reported): array
    {
        $out = [];

        foreach ($reported as $b) {
            if (! is_array($b) || ! isset($b['id']) || ! is_string($b['id'])) {
                continue;
            }

            if (! preg_match(self::BACKUP_ID, $b['id'])) {
                continue;
            }

            $out[] = [
                'id' => $b['id'],
                'size' => (int) ($b['size'] ?? 0),
                'at' => (string) ($b['at'] ?? ''),
                'safety' => ! empty($b['safety']),
            ];
        }

        return $out;
    }

    /**
     * Store console output the agent shipped.
     *
     * @param array<int, mixed> $lines
     */
    public function recordConsole(GarrisonAgent $agent, array $lines): void
    {
        if ($lines === []) {
            return;
        }

        $rows = [];

        foreach ($lines as $line) {
            if (! is_array($line) || ! isset($line['server'], $line['text'])) {
                continue;
            }

            $at = Carbon::now();

            if (! empty($line['at'])) {
                try {
                    $parsed = Carbon::parse($line['at']);

                    // 🚨 Same guard as running_since: a zero time from the far
                    // end parses cleanly and would file a console line under
                    // the year 1, where nothing will ever find it again.
                    if ($parsed->year > 2000) {
                        $at = $parsed;
                    }
                } catch (\Throwable) {
                    // Keep the line, lose the timestamp. Output with an
                    // approximate time beats no output.
                }
            }

            $rows[] = [
                'agent_id' => $agent->id,
                'server_ref' => (string) $line['server'],
                'at' => $at,
                // 🚨 Truncated, not rejected. A single enormous line — a stack
                // trace, a base64 blob a mod decided to log — must not fail the
                // whole insert and lose every other line in the batch with it.
                'text' => mb_substr((string) $line['text'], 0, 4000),
                'stderr' => ! empty($line['stderr']),
            ];
        }

        if ($rows === []) {
            return;
        }

        // One insert for the batch: a row-at-a-time loop turns a chatty server
        // into hundreds of queries per poll.
        $this->db->table('garrison_console')->insert($rows);
    }

    /**
     * Drop console output older than the retention window.
     *
     * 🚨 Pruned on a schedule, because this table grows forever otherwise. A
     * busy server producing a few hundred lines a minute is tens of millions
     * of rows a year — a table that eventually makes the forum's own backups
     * fail, for output nobody will ever read.
     */
    public function pruneConsole(int $keepHours = 48): int
    {
        return $this->db->table('garrison_console')
            ->where('at', '<', Carbon::now()->subHours($keepHours))
            ->delete();
    }

    /**
     * Mark the agent alive and record what it says it can do.
     */
    public function touch(GarrisonAgent $agent, array $info = []): void
    {
        $agent->last_seen_at = Carbon::now();

        if ($info !== []) {
            $agent->version = $info['version'] ?? $agent->version;
            $agent->os = $info['os'] ?? $agent->os;
            $agent->arch = $info['arch'] ?? $agent->arch;

            if (isset($info['drivers']) && is_array($info['drivers'])) {
                $agent->drivers = json_encode(array_values($info['drivers']));
            }
        }

        $agent->updated_at = Carbon::now();
        $agent->save();
    }
}
