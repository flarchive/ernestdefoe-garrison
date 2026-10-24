<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Edition;
use PHPUnit\Framework\TestCase;

/**
 * The free/paid boundary, proved in BOTH directions.
 *
 * 🚨 The standing rule from the anti-piracy rollout, and the reason this file
 * is not just a happy-path test: *a check that can never refuse is decoration,
 * not protection.* Testing only that pro works would pass just as well against
 * a boundary that is open to everybody, which is the failure that actually
 * costs money — and it is invisible, because the product works perfectly.
 *
 * So every assertion here has a twin: the free tier genuinely refuses, AND pro
 * genuinely lifts it.
 */
class EditionTest extends TestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        Edition::resetForTesting();
    }

    protected function tearDown(): void
    {
        /*
         * 🚨 Static state outlives the test. Without this, the first test to
         * enable pro grants it to every test that runs afterwards — and the
         * free-tier assertions below would go green without ever having been
         * true, which is precisely the shape of bug this file exists to catch.
         */
        Edition::resetForTesting();
        parent::tearDown();
    }

    public function testFreeIsTheDefault(): void
    {
        $this->assertFalse(Edition::isPro(), 'an install with no pro package must be free');
    }

    public function testTheFreeTierRefusesEveryPaidVerb(): void
    {
        foreach (Edition::PRO_VERBS as $verb) {
            $this->assertNotContains($verb, Edition::verbs(), "{$verb} must not be queueable for free");
        }
    }

    public function testProAllowsEveryVerb(): void
    {
        Edition::enablePro();

        foreach ([...Edition::FREE_VERBS, ...Edition::PRO_VERBS] as $verb) {
            $this->assertContains($verb, Edition::verbs(), "{$verb} must be queueable with pro");
        }
    }

    public function testTheFreeTierKeepsLifecycleConsoleAndStatus(): void
    {
        // The product has to be worth installing on its own, or the funnel is
        // a nag screen. These are what make a forum able to run a server.
        foreach (['server.start', 'server.stop', 'server.restart', 'server.status', 'console.tail', 'console.send'] as $verb) {
            $this->assertContains($verb, Edition::verbs(), "{$verb} must be free");
        }
    }

    /**
     * 🚨 The health probes ride on `server.status`, and that is deliberate.
     *
     * A free tier that cannot tell an operator their server has stopped
     * accepting players is an advert, not a product — and that exact failure is
     * why Garrison exists. If this ever fails because somebody moved
     * `server.status` into PRO_VERBS, the answer is to move it back.
     */
    public function testTheFreeTierCanStillSeeThatAServerIsUnwell(): void
    {
        $this->assertContains('server.status', Edition::FREE_VERBS);
        $this->assertNotContains('server.status', Edition::PRO_VERBS);
    }

    public function testCapsApplyForFreeAndAreLiftedByPro(): void
    {
        $this->assertSame(1, Edition::maxHosts());
        $this->assertSame(1, Edition::maxServers());

        Edition::enablePro();

        $this->assertNull(Edition::maxHosts(), 'pro must not cap hosts');
        $this->assertNull(Edition::maxServers(), 'pro must not cap servers');
    }

    /**
     * 🚨 The two lists must partition the catalogue exactly.
     *
     * A verb added to Dispatcher::QUEUEABLE and to neither tier would be
     * refused for everybody, including paying customers, and the refusal would
     * say "that is part of Garrison Pro" to somebody who had bought it. A verb
     * in both would be meaningless. Neither is visible by reading one file.
     */
    public function testEveryKnownVerbBelongsToExactlyOneTier(): void
    {
        $free = Edition::FREE_VERBS;
        $pro = Edition::PRO_VERBS;

        $this->assertSame([], array_intersect($free, $pro), 'a verb cannot be in both tiers');

        sort($free);
        sort($pro);
        $all = [...$free, ...$pro];
        sort($all);

        $catalogue = Dispatcher::QUEUEABLE;
        sort($catalogue);

        $this->assertSame($catalogue, $all, 'FREE_VERBS + PRO_VERBS must equal Dispatcher::QUEUEABLE');
    }

    public function testEveryDestructiveVerbIsPaid(): void
    {
        // Restoring and deleting backups are the two verbs that can lose work.
        // They are also both pro; if that ever stops being true it should be a
        // decision, not a drift.
        foreach (Dispatcher::DESTRUCTIVE as $verb) {
            $this->assertContains($verb, Edition::PRO_VERBS, "{$verb} is destructive and should be paid");
        }
    }
}
