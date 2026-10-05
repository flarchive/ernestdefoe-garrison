<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use PHPUnit\Framework\TestCase;

/**
 * The permission method's ORDER, which is not something reading it proves.
 *
 * 🚨 The provisioning check first sat above the `garrison.manage` early return
 * and refused everybody, administrators included — because every branch in that
 * method runs on the path where manage is ABSENT. The code looked right in
 * isolation; only its position was wrong.
 *
 * An ordering mistake in a permission check reads as a broken feature rather
 * than as a security bug, which is exactly how it survives review: somebody
 * clicks the button, it says no, and they go and look at the button.
 */
class DispatcherOrderTest extends TestCase
{
    private function source(): string
    {
        return (string) file_get_contents(__DIR__ . '/../src/Agent/Dispatcher.php');
    }

    private function positionOf(string $needle): int
    {
        $at = strpos($this->source(), $needle);

        $this->assertNotFalse($at, "could not find {$needle} in Dispatcher");

        return $at;
    }

    public function testProvisioningIsCheckedAfterTheManageShortcut(): void
    {
        $manage = $this->positionOf("if (\$actor->hasPermission('garrison.manage')) {");
        $provision = $this->positionOf("str_starts_with(\$verb, 'provision.')");

        $this->assertGreaterThan(
            $manage,
            $provision,
            'the provisioning check sits above the manage shortcut, so it refuses administrators too'
        );
    }

    /**
     * 🚨 The opposite ordering, for the one verb that genuinely must come
     * first. player.verify is the only thing an ordinary member may queue, and
     * every rule below the manage shortcut would refuse it.
     */
    public function testPlayerVerifyIsCheckedBeforeTheManageShortcut(): void
    {
        $manage = $this->positionOf("if (\$actor->hasPermission('garrison.manage')) {");
        $verify = $this->positionOf("if (\$verb === 'player.verify') {");

        $this->assertLessThan(
            $manage,
            $verify,
            'player.verify is checked after the manage shortcut, so ordinary members can never link an account'
        );
    }

    /**
     * Every verb the forum will queue has to be one the AGENT implements.
     * The two halves repeat the set on purpose — see Dispatcher's docblock —
     * and a verb in one and not the other is a control that can only ever
     * fail.
     */
    public function testEveryQueueableVerbIsInTheAgentsSet(): void
    {
        $agentVerbs = $this->agentVerbs();

        foreach (Dispatcher::QUEUEABLE as $verb) {
            $this->assertContains(
                $verb,
                $agentVerbs,
                "the forum will queue {$verb}, which the agent does not implement"
            );
        }
    }

    public function testDestructiveAndMutatingVerbsAreQueueable(): void
    {
        foreach ([...Dispatcher::MUTATING, ...Dispatcher::DESTRUCTIVE] as $verb) {
            $this->assertContains(
                $verb,
                Dispatcher::QUEUEABLE,
                "{$verb} is classified but not queueable, so its classification does nothing"
            );
        }
    }

    /**
     * Reads the closed verb set out of the agent's own source.
     *
     * 🚨 The two halves of this product are written in different languages and
     * cannot share a constant, so the only way to keep them in step is to read
     * one from the other. The alternative is a second hand-maintained list,
     * which is the thing that drifts.
     *
     * @return array<int, string>
     */
    private function agentVerbs(): array
    {
        $source = (string) file_get_contents(__DIR__ . '/../agent/internal/protocol/protocol.go');

        preg_match_all('/Verb\s*=\s*"([a-z.]+)"/', $source, $matches);

        $this->assertNotEmpty($matches[1], 'no verbs were found in the agent source');

        return $matches[1];
    }
}
