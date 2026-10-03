<?php

namespace ErnestDefoe\Garrison\Health;

use Carbon\Carbon;
use Flarum\Settings\SettingsRepositoryInterface;
use Illuminate\Contracts\Queue\Queue;

/**
 * Proves that Garrison's machinery is actually running, by running it.
 *
 * 🚨 THIS IS THE PRODUCT WATCHING ITSELF, AND IT IS NOT DECORATION.
 *
 * Garrison rests on two pieces of forum infrastructure that it does not own and
 * cannot start:
 *
 *   The SCHEDULER. `php flarum schedule:run` every minute is what runs the
 *   health ladder, the backup schedule, the restart schedule and this check.
 *   On a forum where nobody set up that cron entry — which is most shared
 *   hosting, and a good number of VPS installs — Garrison is completely inert.
 *   Servers still show their last known state, the panel still looks right, and
 *   nothing is ever checked, restarted, backed up or reported. There is no
 *   error anywhere, because nothing ran to produce one.
 *
 *   The QUEUE WORKER. Every notification Garrison sends is a job. With no
 *   worker, `sync()` returns cleanly, nothing is logged, and not one
 *   notification is written. The panel stays green through the outage this
 *   product exists to catch.
 *
 * Neither failure announces itself, and both look exactly like "nothing has
 * gone wrong lately". That is not hypothetical: the queue worker was dead for
 * three days on the forum this extension was built on, and the only reason it
 * was found was somebody going looking.
 *
 * So Garrison refuses to assume. The scheduled health tick stamps PUSHED and
 * pushes a job; a worker running that job stamps RAN. Two timestamps, and the
 * gap between them and now answers both questions — not "a worker looks
 * configured" but "a job pushed a minute ago has been run", which is the same
 * sentence an alert needs to be true.
 */
class Heartbeat
{
    /**
     * When the heartbeat was last PUSHED — written by the scheduled command,
     * so it is also proof the scheduler itself ran.
     */
    public const PUSHED = 'ernestdefoe-garrison.queue_pushed_at';

    /** When a worker last RAN one. */
    public const RAN = 'ernestdefoe-garrison.queue_ran_at';

    /**
     * How far behind either may fall before Garrison says so.
     *
     * 🚨 Generous on purpose — five minutes against a one-minute heartbeat.
     * A worker restarting during a deploy, a long job holding the queue, a host
     * under momentary load: none of those mean anything is broken, and a
     * warning that cries wolf on every deploy is a warning an operator learns
     * to scroll past. What this must catch is the cron entry nobody ever added
     * and the worker that has been dead since Tuesday.
     */
    public const STALE_SECONDS = 300;

    public function __construct(
        protected Queue $queue,
        protected SettingsRepositoryInterface $settings
    ) {
    }

    /**
     * Push one heartbeat. Called from the scheduled health command, so it runs
     * on the same tick as everything else rather than needing its own schedule.
     */
    public function beat(): void
    {
        $this->settings->set(self::PUSHED, Carbon::now()->toIso8601String());

        $this->queue->push(new HeartbeatJob());
    }

    /**
     * What to tell the operator.
     *
     * 🚨 TWO separate verdicts, because they have two completely different
     * fixes — one is a cron entry, the other is a worker process — and folding
     * them into a single "something is wrong" would send an operator to look
     * at the wrong thing half the time.
     *
     * @return array{
     *     scheduler: array{state: string, at: ?string},
     *     queue: array{state: string, at: ?string, behindSeconds: ?int}
     * }
     */
    public function report(): array
    {
        $pushed = $this->at(self::PUSHED);
        $ran = $this->at(self::RAN);

        return [
            'scheduler' => [
                'state' => $this->freshness($pushed),
                'at' => $pushed?->toIso8601String(),
            ],
            'queue' => [
                /*
                 * 🚨 A queue cannot be judged until the scheduler is working.
                 *
                 * With no scheduler nothing is ever PUSHED, so nothing is ever
                 * run, and a naive check would report the worker as stalled as
                 * well — sending an operator to restart a queue worker that is
                 * fine, while the actual cause sits one line above, correctly
                 * diagnosed and ignored because two alarms are going off.
                 */
                'state' => $this->freshness($pushed) === 'ok' ? $this->freshness($ran) : 'unknown',
                'at' => $ran?->toIso8601String(),

                /*
                 * 🚨 Plain timestamp arithmetic, not Carbon's diffInSeconds.
                 *
                 * Carbon reversed the sign of a signed diff between v2 and v3,
                 * so `$ran->diffInSeconds($pushed, false)` means opposite
                 * things on the two versions a customer might have in vendor/.
                 * The first cut of this line got it backwards and reported a
                 * worker twelve minutes behind as zero seconds behind — a
                 * number that looks healthy, which is the worst way for a
                 * health figure to be wrong.
                 */
                'behindSeconds' => $pushed && $ran ? max(0, $pushed->getTimestamp() - $ran->getTimestamp()) : null,
            ],
        ];
    }

    /**
     * ok | stalled | unknown
     *
     * 🚨 `unknown` is a real answer and deliberately not `stalled`.
     *
     * Before the first tick has run — a forum that installed Garrison ninety
     * seconds ago — there is no evidence either way, and claiming something is
     * broken on no evidence teaches an operator that this warning means
     * nothing.
     */
    protected function freshness(?Carbon $at): string
    {
        if ($at === null) {
            return 'unknown';
        }

        return $at->gt(Carbon::now()->subSeconds(self::STALE_SECONDS)) ? 'ok' : 'stalled';
    }

    protected function at(string $key): ?Carbon
    {
        $raw = $this->settings->get($key);

        if (empty($raw)) {
            return null;
        }

        try {
            return Carbon::parse($raw);
        } catch (\Throwable) {
            // A setting somebody edited by hand is not worth a 500 on the
            // admin page that would show them what they broke.
            return null;
        }
    }
}
