<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\Gateway;
use PHPUnit\Framework\TestCase;

/**
 * 🚨 That the forum READS the agent's event stream at all.
 *
 * It did not, from the first release until 1.2.2. The agent shipped events in
 * every poll, the forum answered 200, and threw them away — so an install that
 * failed in its background goroutine was indistinguishable from one that
 * succeeded: the command returned `started: true` and then nothing ever
 * happened again.
 *
 * Nothing about that was visible in a log, a status or a test. It was found by
 * installing a game on a real host and watching a directory stay empty.
 */
class AgentEventsReachTheConsoleTest extends TestCase
{
    public function testThePollControllerReadsEvents(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Api/Controller/AgentPollController.php');

        $this->assertStringContainsString(
            "\$body['events']",
            $source,
            'the poll controller must read the event stream — the agent has always sent it'
        );
        $this->assertStringContainsString('recordEvents(', $source);
    }

    public function testEveryKeyTheAgentSendsIsConsumed(): void
    {
        /*
         * 🚨 The real guard. `events` was missed because nothing compared what
         * the agent sends against what the forum reads — each key was added on
         * its own and one was simply never wired.
         *
         * The agent's pollRequest declares: info, servers, results, events,
         * console. If a key is added there, it must be read here too, or it
         * will be accepted and discarded exactly as events were.
         */
        $source = (string) file_get_contents(__DIR__ . '/../src/Api/Controller/AgentPollController.php');

        foreach (['info', 'servers', 'results', 'events', 'console'] as $key) {
            $this->assertStringContainsString(
                "\$body['{$key}']",
                $source,
                "the agent sends '{$key}' in every poll and nothing here reads it"
            );
        }
    }

    public function testConsoleEventsAreFoldedIntoTheConsole(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Agent/Gateway.php');

        // recordEvents must delegate to recordConsole rather than writing its
        // own rows: two writers would drift on truncation and timestamps.
        $this->assertMatchesRegularExpression(
            '/function recordEvents.*?\$this->recordConsole\(/s',
            $source,
            'recordEvents should reuse recordConsole so one place decides how a line is written'
        );
    }

    public function testRecordEventsIsPublicAndTakesAnArray(): void
    {
        $r = new \ReflectionMethod(Gateway::class, 'recordEvents');

        $this->assertTrue($r->isPublic());
        $this->assertSame(2, $r->getNumberOfParameters());
    }
}
