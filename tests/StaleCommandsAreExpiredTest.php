<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use PHPUnit\Framework\TestCase;

/**
 * 🚨 That `expireStale` is actually CALLED, not merely correct.
 *
 * It existed from the first release, with a careful docblock about commands
 * that would otherwise "say delivered for ever, which reads as in progress on a
 * dashboard and makes an operator wait for something that will never end" — and
 * nothing anywhere invoked it. The method was right; the feature was dead.
 *
 * It surfaced the honest way: restarting an agent while a command was in flight
 * left a row on a live forum stuck at `delivered` with nothing to clear it.
 *
 * So this asserts the WIRING rather than the behaviour. A unit test of
 * expireStale itself would have passed every day it was unreachable.
 */
class StaleCommandsAreExpiredTest extends TestCase
{
    public function testTheScheduledHealthCommandExpiresThem(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Console/HealthCommand.php');

        $this->assertStringContainsString(
            'expireStale()',
            $source,
            'HealthCommand must expire stale commands — it is the only thing that runs every minute'
        );
    }

    public function testNothingElseIsExpectedToCallIt(): void
    {
        // If a second caller appears, that is fine — but it should be a
        // decision, not two schedules quietly racing to expire the same rows.
        $callers = [];

        foreach (glob(__DIR__ . '/../src/**/*.php') ?: [] as $file) {
            if (str_contains((string) file_get_contents($file), 'expireStale(')) {
                $callers[] = basename($file);
            }
        }

        sort($callers);

        $this->assertSame(
            ['Dispatcher.php', 'HealthCommand.php'],
            $callers,
            'expireStale should be defined in Dispatcher and called from HealthCommand, and nowhere else'
        );
    }

    public function testTheDefaultWindowIsLongerThanAPollCycle(): void
    {
        $r = new \ReflectionMethod(Dispatcher::class, 'expireStale');
        $default = $r->getParameters()[0]->getDefaultValue();

        // An agent long-polls and answers on a later cycle. Expiring faster
        // than that would cancel commands that were about to succeed, which is
        // worse than leaving one showing as in progress.
        $this->assertGreaterThanOrEqual(120, $default, 'the expiry window must outlast a normal poll cycle');
    }
}
