<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;

/**
 * One piece of scheduled work against one server.
 *
 * 🚨 This class decides WHEN, and nothing else. It does not queue commands, it
 * does not talk to an agent, and it does not know what a restart is. The
 * runner asks it two questions — is this due, is a warning due — and acts on
 * the answers. Keeping the arithmetic here is what makes it testable without a
 * database, a forum, or a host.
 */
class Schedule extends AbstractModel
{
    protected $table = 'garrison_schedules';

    /**
     * 🚨 Every datetime, because a missing cast comes back as a string and
     * the first Carbon call on it is a fatal on a path that may not run for
     * days. That exact bug killed the health command for two days over
     * `last_remediation_at`.
     */
    protected $casts = [
        'enabled' => 'bool',
        'last_run_at' => 'datetime',
        'last_warned_at' => 'datetime',
        'created_at' => 'datetime',
        'updated_at' => 'datetime',
    ];

    public const KINDS = ['restart', 'backup', 'console'];

    public function server()
    {
        return $this->belongsTo(Server::class, 'server_id');
    }

    /**
     * Is this schedule's action due right now?
     *
     * 🚨 The window is the CALLER's tick, and lateness is forgiven up to it.
     *
     * The scheduler runs every minute, and a minute that is skipped — a busy
     * host, a cron that overlapped, a deploy — must not silently drop a
     * nightly restart until tomorrow. So "due" means the target time has
     * passed today and nothing has run since, rather than "the clock reads
     * exactly 05:00".
     *
     * The other half of that forgiveness is the cutoff: a schedule missed for
     * six hours should NOT fire at 11am, because a restart nobody expects is
     * worse than a restart that did not happen. GRACE_MINUTES is how late is
     * still worth doing.
     */
    public const GRACE_MINUTES = 30;

    public function isDue(?Carbon $now = null): bool
    {
        return $this->dueAt($this->at_minute, $this->last_run_at, $now);
    }

    /**
     * Is the advance warning due? Only meaningful when warn_minutes is set.
     *
     * 🚨 Tracked separately from the run, because a warning that marked the
     * schedule as run would announce a restart every night and never restart.
     */
    public function isWarningDue(?Carbon $now = null): bool
    {
        if ((int) $this->warn_minutes <= 0) {
            return false;
        }

        $at = (int) $this->at_minute - (int) $this->warn_minutes;

        /*
         * 🚨 A warning for a 00:10 restart lands at 23:50 the day BEFORE, and
         * on the previous day's mask. Wrapping the clock without wrapping the
         * day would announce Monday's restart on Monday night rather than
         * Sunday night — so a negative offset is simply not warned, rather
         * than warned at the wrong time.
         *
         * Refusing is the honest failure: an operator who wants a warning
         * across midnight can move the restart, and a warning that arrives
         * twenty-four hours early would be read as a bug in the server.
         */
        if ($at < 0) {
            return false;
        }

        return $this->dueAt($at, $this->last_warned_at, $now);
    }

    /**
     * The shared arithmetic. $mark is whichever timestamp records that this
     * particular thing already happened.
     */
    protected function dueAt(int $minute, ?Carbon $mark, ?Carbon $now): bool
    {
        if (! $this->enabled) {
            return false;
        }

        $local = ($now ?? Carbon::now())->copy()->setTimezone($this->zone());

        if (! $this->runsOn($local)) {
            return false;
        }

        $target = $local->copy()->startOfDay()->addMinutes($minute);

        if ($local->lt($target)) {
            return false;
        }

        if ($local->gt($target->copy()->addMinutes(self::GRACE_MINUTES))) {
            return false;
        }

        // Already done for this occurrence.
        return $mark === null || $mark->copy()->setTimezone($this->zone())->lt($target);
    }

    /**
     * 🚨 Monday first, matching the mask's documented order and ISO 8601 —
     * NOT Carbon's `dayOfWeek`, which is 0 = Sunday. Getting this backwards
     * shifts every schedule by a day, and the symptom is a restart that
     * happens on the wrong night, which nobody notices for a week.
     */
    protected function runsOn(Carbon $local): bool
    {
        $mask = str_pad((string) $this->days, 7, '0');

        return ($mask[$local->dayOfWeekIso - 1] ?? '0') === '1';
    }

    /**
     * 🚨 An invalid timezone falls back to UTC rather than throwing.
     *
     * The string comes from an admin form and from whatever a database had in
     * it before this column existed. Carbon throws on an unknown zone, and a
     * throw here happens inside the scheduler — which, per this codebase's
     * history, means exit 255 with no output and every OTHER schedule on the
     * forum silently stopping too.
     */
    protected function zone(): string
    {
        $tz = (string) ($this->timezone ?: 'UTC');

        return in_array($tz, timezone_identifiers_list(), true) ? $tz : 'UTC';
    }

    /**
     * The verb and params this schedule turns into, or null if it cannot make
     * one. Used for both the action and its warning.
     */
    public function asCommand(bool $warning = false): ?array
    {
        if ($warning) {
            $line = trim((string) $this->warn_payload);

            return $line === '' ? null : ['console.send', ['line' => $line]];
        }

        return match ($this->kind) {
            'restart' => ['server.restart', []],
            'backup' => ['backup.create', []],
            'console' => ($line = trim((string) $this->payload)) === ''
                ? null
                : ['console.send', ['line' => $line]],
            default => null,
        };
    }

    /**
     * One schedule, shaped for the admin panel.
     *
     * 🚨 On the MODEL rather than in a controller, because two packages need
     * the identical shape: garrison reports existing schedules from its admin
     * endpoint so that removing pro leaves them visible and paused, and
     * garrison-pro echoes a row back after creating or editing one. The same
     * JSON built in two repositories would drift, and the drift would show up
     * as a field that is silently absent in one of the two paths.
     */
    public function toAdminArray(): array
    {
        return [
            'id' => $this->id,
            'serverId' => $this->server_id,
            'kind' => $this->kind,
            'atMinute' => (int) $this->at_minute,
            'days' => $this->days,
            'timezone' => $this->timezone,
            'payload' => $this->payload,
            'warnMinutes' => (int) $this->warn_minutes,
            'warnPayload' => $this->warn_payload,
            'enabled' => (bool) $this->enabled,
            'lastRunAt' => $this->last_run_at?->toIso8601String(),
        ];
    }
}
