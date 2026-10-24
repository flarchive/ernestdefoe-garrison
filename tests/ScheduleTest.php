<?php

namespace ErnestDefoe\Garrison\Tests;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Model\Schedule;
use PHPUnit\Framework\TestCase;

/**
 * The arithmetic that decides whether a restart happens tonight.
 *
 * 🚨 This is the suite that matters most in the PHP half, because every bug it
 * can catch is INVISIBLE. A day-of-week off-by-one restarts a server on the
 * wrong night and nobody notices for a week. A timezone read from the database
 * instead of the operator's choice restarts it at the wrong hour, in the
 * middle of the evening, and reads as a crash. A shared last-run mark makes a
 * server announce a restart every night and never restart. None of those throw,
 * none appear in a log, and all three are one character away from the code
 * that is here.
 *
 * No database: a Schedule is an Eloquent model, and setting attributes on one
 * needs no connection. That is the point — arithmetic this consequential
 * should be provable in milliseconds so that it actually gets run.
 */
class ScheduleTest extends TestCase
{
    private function schedule(array $attributes = []): Schedule
    {
        $s = new Schedule();

        $s->forceFill(array_merge([
            'server_id' => 1,
            'kind' => 'restart',
            'at_minute' => 5 * 60, // 05:00
            'days' => '1111111',
            'timezone' => 'UTC',
            'warn_minutes' => 0,
            'enabled' => true,
            'last_run_at' => null,
            'last_warned_at' => null,
        ], $attributes));

        return $s;
    }

    private function at(string $iso, string $tz = 'UTC'): Carbon
    {
        return Carbon::parse($iso, $tz);
    }

    public function testFiresAtItsTime(): void
    {
        $this->assertTrue($this->schedule()->isDue($this->at('2026-09-15 05:00:30')));
    }

    public function testDoesNotFireBeforeItsTime(): void
    {
        $this->assertFalse($this->schedule()->isDue($this->at('2026-09-15 04:59:00')));
    }

    /**
     * 🚨 A skipped minute must not silently drop a nightly restart until
     * tomorrow. Busy hosts, overlapping crons and deploys all skip minutes.
     */
    public function testForgivesALateTick(): void
    {
        $this->assertTrue($this->schedule()->isDue($this->at('2026-09-15 05:20:00')));
    }

    /**
     * 🚨 And the other half: a schedule missed for hours must NOT fire at
     * lunchtime. A restart nobody expects is worse than one that did not
     * happen.
     */
    public function testGivesUpOnceItIsTooLate(): void
    {
        $this->assertFalse($this->schedule()->isDue($this->at('2026-09-15 11:00:00')));
    }

    public function testDoesNotFireTwiceForTheSameOccurrence(): void
    {
        $s = $this->schedule(['last_run_at' => $this->at('2026-09-15 05:00:10')]);

        $this->assertFalse($s->isDue($this->at('2026-09-15 05:05:00')));
    }

    public function testFiresAgainTheNextDay(): void
    {
        $s = $this->schedule(['last_run_at' => $this->at('2026-09-15 05:00:10')]);

        $this->assertTrue($s->isDue($this->at('2026-09-16 05:00:10')));
    }

    public function testDisabledNeverFires(): void
    {
        $this->assertFalse($this->schedule(['enabled' => false])->isDue($this->at('2026-09-15 05:00:30')));
    }

    /**
     * 🚨 THE OFF-BY-ONE.
     *
     * The mask is Monday-first, matching ISO 8601. Carbon's `dayOfWeek` is
     * Sunday-first, and using it shifts every schedule by a day — a restart
     * that happens on the wrong night, which nobody catches for a week because
     * it still happens EVERY night as far as anybody watching can tell.
     *
     * 2026-09-14 is a Monday; 2026-09-20 is a Sunday.
     */
    public function testMondayOnlyFiresOnMonday(): void
    {
        $mondayOnly = $this->schedule(['days' => '1000000']);

        $this->assertTrue($mondayOnly->isDue($this->at('2026-09-14 05:00:30')), 'Monday');
        $this->assertFalse($mondayOnly->isDue($this->at('2026-09-15 05:00:30')), 'Tuesday');
        $this->assertFalse($mondayOnly->isDue($this->at('2026-09-20 05:00:30')), 'Sunday');
    }

    public function testSundayOnlyFiresOnSunday(): void
    {
        $sundayOnly = $this->schedule(['days' => '0000001']);

        $this->assertTrue($sundayOnly->isDue($this->at('2026-09-20 05:00:30')), 'Sunday');
        $this->assertFalse($sundayOnly->isDue($this->at('2026-09-14 05:00:30')), 'Monday');
    }

    /**
     * 🚨 The operator's zone, not the server's and not the database's.
     *
     * 05:00 New York is 09:00 UTC. A schedule that used the host's clock would
     * restart a community's server in the middle of their evening, and the
     * only symptom is players being disconnected at a plausible-looking hour.
     */
    public function testUsesTheSchedulesOwnTimezone(): void
    {
        $s = $this->schedule(['timezone' => 'America/New_York']);

        $this->assertTrue($s->isDue($this->at('2026-09-15 09:00:30')), '05:00 in New York');
        $this->assertFalse($s->isDue($this->at('2026-09-15 05:00:30')), '05:00 UTC is 01:00 there');
    }

    /**
     * 🚨 An unknown zone falls back to UTC rather than throwing. The string
     * comes from an admin form, and a throw would happen inside the scheduler —
     * which in this stack means exit 255, no output, and every OTHER schedule
     * on the forum silently stopping too.
     */
    public function testAnInvalidTimezoneDoesNotThrow(): void
    {
        $s = $this->schedule(['timezone' => 'Mars/Olympus_Mons']);

        $this->assertTrue($s->isDue($this->at('2026-09-15 05:00:30')));
    }

    public function testWarningFiresBeforeTheAction(): void
    {
        $s = $this->schedule(['warn_minutes' => 15, 'warn_payload' => 'say Restarting in 15 minutes']);

        $this->assertTrue($s->isWarningDue($this->at('2026-09-15 04:45:10')), 'the warning');
        $this->assertFalse($s->isDue($this->at('2026-09-15 04:45:10')), 'not the restart yet');
    }

    /**
     * 🚨 THE ONE THAT MAKES A SERVER ANNOUNCE A RESTART AND NEVER RESTART.
     *
     * The warning and the action need separate marks. Sharing one means the
     * warning at 04:45 records "this schedule ran today", and the restart at
     * 05:00 is skipped — every night, for ever, with players being told a
     * restart is coming that never arrives.
     */
    public function testAWarningDoesNotSatisfyTheAction(): void
    {
        $s = $this->schedule([
            'warn_minutes' => 15,
            'warn_payload' => 'say Restarting in 15 minutes',
            'last_warned_at' => $this->at('2026-09-15 04:45:10'),
        ]);

        $this->assertFalse($s->isWarningDue($this->at('2026-09-15 04:50:00')), 'warning already sent');
        $this->assertTrue($s->isDue($this->at('2026-09-15 05:00:30')), 'the restart must still fire');
    }

    /**
     * 🚨 A warning that would land before midnight is refused rather than sent
     * at the wrong time. Wrapping the clock without wrapping the day would
     * announce Monday's restart on Monday night instead of Sunday night — a
     * message twenty-four hours early, which reads as a bug in the game server.
     */
    public function testAWarningThatWouldCrossMidnightIsNotSent(): void
    {
        $s = $this->schedule([
            'at_minute' => 10, // 00:10
            'warn_minutes' => 30,
            'warn_payload' => 'say Restarting soon',
        ]);

        $this->assertFalse($s->isWarningDue($this->at('2026-09-14 23:40:10')));
        $this->assertFalse($s->isWarningDue($this->at('2026-09-15 23:40:10')));
        $this->assertTrue($s->isDue($this->at('2026-09-15 00:10:30')), 'the restart itself still fires');
    }

    public function testNoWarningConfiguredMeansNoWarning(): void
    {
        $this->assertFalse($this->schedule()->isWarningDue($this->at('2026-09-15 04:45:10')));
    }

    public function testEachKindBecomesTheRightVerb(): void
    {
        $this->assertSame('server.restart', $this->schedule(['kind' => 'restart'])->asCommand()[0]);
        $this->assertSame('backup.create', $this->schedule(['kind' => 'backup'])->asCommand()[0]);

        $console = $this->schedule(['kind' => 'console', 'payload' => 'save-all'])->asCommand();
        $this->assertSame('console.send', $console[0]);
        $this->assertSame(['line' => 'save-all'], $console[1]);
    }

    /**
     * 🚨 A console schedule with nothing to say produces no command at all,
     * rather than sending an empty line to a game server. Some games treat a
     * bare newline as a command terminator and others log it as an error; both
     * are noise nobody asked for, every night.
     */
    public function testAnEmptyConsoleScheduleProducesNothing(): void
    {
        $this->assertNull($this->schedule(['kind' => 'console', 'payload' => '   '])->asCommand());
        $this->assertNull($this->schedule(['kind' => 'console', 'payload' => null])->asCommand());
    }

    public function testAnUnknownKindProducesNothing(): void
    {
        $this->assertNull($this->schedule(['kind' => 'nonsense'])->asCommand());
    }
}
