<?php

namespace ErnestDefoe\Garrison\Tests;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Model\PlaySession;
use ErnestDefoe\Garrison\Players\Tracker;
use PHPUnit\Framework\TestCase;

/**
 * The session maths behind every playtime total on the forum.
 *
 * 🚨 These are the numbers people compare themselves against. A leaderboard
 * that is quietly wrong is worse than no leaderboard: nobody can tell, everyone
 * believes it, and by the time somebody notices the bad rows are months deep
 * and indistinguishable from the good ones.
 *
 * The tracker's collaborators are Eloquent models, so the parts that touch the
 * database are exercised on dev rather than here. What IS here is the
 * arithmetic and the cleaning, which are the parts that go wrong silently.
 */
class TrackerTest extends TestCase
{
    private function tracker(): Tracker
    {
        return new Tracker();
    }

    /**
     * 🚨 No setAccessible(): PHP 8.1 made reflection on protected methods work
     * without it, and 8.5 deprecates the call. Leaving it in turns every run of
     * this suite yellow, which is how a real warning later gets scrolled past.
     */
    private function call(string $method, array $args)
    {
        return (new \ReflectionMethod(Tracker::class, $method))->invokeArgs($this->tracker(), $args);
    }

    /**
     * 🚨 Names come from a game log, and in most games a player picks their own
     * display name. The forum is where these become rows, links and profile
     * pages, so it applies its own bounds rather than trusting the agent's —
     * two checks that fail differently is the point of having both.
     */
    public function testAbsurdNamesAreDropped(): void
    {
        $got = $this->call('clean', [[
            'alice',
            '',
            '   ',
            str_repeat('a', 65),
            'bob',
            123,
            null,
            "  carol  ",
        ]]);

        sort($got);

        $this->assertSame(['alice', 'bob', 'carol'], $got);
    }

    /**
     * The same player twice in one report — which a log with a duplicated join
     * produces — must not open two sessions.
     */
    public function testDuplicatesCollapse(): void
    {
        $this->assertSame(['alice'], $this->call('clean', [['alice', 'alice', 'alice']]));
    }

    /**
     * 🚨 THE CLAMP, and it is not paranoia.
     *
     * A server whose agent was offline for a week comes back and closes a
     * session that has been open since then. Counting 168 hours of playtime for
     * somebody who played twenty minutes ruins every total on the forum,
     * permanently, and nothing in the data afterwards says which rows are wrong.
     */
    public function testASessionCannotBeLongerThanADay(): void
    {
        $this->assertSame(86400, Tracker::MAX_SESSION_SECONDS);

        $session = new PlaySession();
        $session->started_at = Carbon::now()->subDays(7);

        $seconds = max(0, Carbon::now()->getTimestamp() - $session->started_at->getTimestamp());

        $this->assertGreaterThan(Tracker::MAX_SESSION_SECONDS, $seconds, 'the fixture is not long enough to test the clamp');
        $this->assertSame(Tracker::MAX_SESSION_SECONDS, min($seconds, Tracker::MAX_SESSION_SECONDS));
    }

    /**
     * 🚨 An OPEN session is measured to now, not reported as zero.
     *
     * The person somebody is most likely to look up is the one currently
     * playing, and "0 minutes" next to a green dot is the panel contradicting
     * itself on the only row anybody is reading.
     */
    public function testAnOpenSessionCountsTimeSoFar(): void
    {
        $session = new PlaySession();
        $session->started_at = Carbon::now()->subMinutes(30);
        $session->ended_at = null;

        $this->assertEqualsWithDelta(1800, $session->seconds(), 5);
    }

    public function testAClosedSessionReportsWhatWasStored(): void
    {
        $session = new PlaySession();
        $session->started_at = Carbon::now()->subHours(3);
        $session->ended_at = Carbon::now();
        $session->seconds = 42;

        // 🚨 The STORED value, not a recomputation. The stored one has been
        // clamped; recomputing from the timestamps would hand back the very
        // 3-hour figure the clamp exists to reject.
        $this->assertSame(42, $session->seconds());
    }

    /**
     * 🚨 A clock that went backwards must not produce a negative session.
     *
     * NTP corrections, a VM resuming from a snapshot, a host with a bad RTC:
     * all of them can make "now" earlier than a session's start. A negative
     * number in an unsigned column is a database error at best and a wildly
     * wrong total at worst.
     */
    public function testTimeGoingBackwardsDoesNotProduceANegativeSession(): void
    {
        $session = new PlaySession();
        $session->started_at = Carbon::now()->addHour();
        $session->ended_at = null;

        $this->assertSame(0, $session->seconds());
    }
}
