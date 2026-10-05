<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Edition;
use ErnestDefoe\Garrison\Entitled;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Foundation\ValidationException;
use Flarum\Locale\TranslatorInterface;
use Flarum\User\User;
use PHPUnit\Framework\TestCase;

/**
 * What the generic queue endpoint refuses, driven through `queue()` exactly as
 * QueueCommandController calls it — no source argument, so the default.
 *
 * 🚨 Each of these was a hole on the shipped code:
 *
 * - `console.tail` needed only `garrison.view`, so a viewer read the whole
 *   console (chat, IPs, admin commands) that ConsoleController refuses them.
 * - `console.tail` with `follow` opened a log follower on the game host that
 *   nothing ever closed.
 * - `player.verify` with the browser's own params let any member send a
 *   server-attributed message of their wording to any online player.
 */
class DispatcherRefusesTest extends TestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        Edition::resetForTesting();
        Edition::enablePro();
    }

    protected function tearDown(): void
    {
        Edition::resetForTesting();
        parent::tearDown();
    }

    private function dispatcher(): ExposedDispatcher
    {
        $translator = $this->createStub(TranslatorInterface::class);
        $translator->method('trans')->willReturnArgument(0);

        $entitled = $this->createStub(Entitled::class);
        $entitled->method('allowsServer')->willReturn(true);

        return new ExposedDispatcher($translator, $entitled);
    }

    /** @param array<int, string> $permissions */
    private function actor(array $permissions): User
    {
        $user = $this->createStub(User::class);
        $user->method('hasPermission')->willReturnCallback(fn (string $p) => in_array($p, $permissions, true));
        $user->method('isGuest')->willReturn(false);
        $user->id = 7;

        return $user;
    }

    private function server(): Server
    {
        $server = new Server();
        $server->id = 1;
        $server->is_public = true;
        $server->agent_id = 1;
        $server->ref = 'valheim';

        return $server;
    }

    private function assertRefused(callable $queue, string $reason): void
    {
        try {
            $queue();
        } catch (ValidationException $e) {
            $this->assertSame($reason, $e->getAttributes()['verb'] ?? null);

            return;
        }

        $this->fail("queued; expected a refusal with {$reason}");
    }

    public function testAViewerCannotReadTheConsoleThroughTheQueue(): void
    {
        $this->assertRefused(
            fn () => $this->dispatcher()->queue($this->actor(['garrison.view']), $this->server(), 'console.tail', ['history' => 100000]),
            'ernestdefoe-garrison.api.errors.not_permitted'
        );

        // Controlling a server is not reading its console either.
        $this->assertRefused(
            fn () => $this->dispatcher()->queue($this->actor(['garrison.view', 'garrison.control']), $this->server(), 'console.tail'),
            'ernestdefoe-garrison.api.errors.not_permitted'
        );
    }

    public function testConsoleHoldersAndManagersMayStillTail(): void
    {
        $d = $this->dispatcher();

        $d->permitted($this->actor(['garrison.view', 'garrison.console']), $this->server(), 'console.tail');
        $d->permitted($this->actor(['garrison.manage']), $this->server(), 'console.tail');

        $this->addToAssertionCount(2);
    }

    public function testTheQueueNeverOpensAFollowingTail(): void
    {
        // Not even for an administrator: nothing on the forum closes one.
        $this->assertRefused(
            fn () => $this->dispatcher()->queue($this->actor(['garrison.manage']), $this->server(), 'console.tail', ['follow' => true]),
            'ernestdefoe-garrison.api.errors.no_follow'
        );
    }

    public function testTailHistoryIsCappedAndNothingElseRidesAlong(): void
    {
        $d = $this->dispatcher();

        $this->assertSame(['history' => Dispatcher::TAIL_HISTORY_MAX], $d->bounded('console.tail', ['history' => 100000, 'extra' => 'x']));
        $this->assertSame(['history' => 1], $d->bounded('console.tail', ['history' => -5]));
        $this->assertSame(['history' => 200], $d->bounded('console.tail', []));

        // Other verbs' params are untouched.
        $this->assertSame(['line' => 'say hi'], $d->bounded('console.send', ['line' => 'say hi']));
    }

    public function testAMemberCannotQueuePlayerVerifyThemselves(): void
    {
        $this->assertRefused(
            fn () => $this->dispatcher()->queue($this->actor([]), $this->server(), 'player.verify', ['player' => 'alice', 'code' => 'visit evil.example']),
            'ernestdefoe-garrison.api.errors.not_permitted'
        );

        // Nor an administrator: the Linker is the only author of a code.
        $this->assertRefused(
            fn () => $this->dispatcher()->queue($this->actor(['garrison.manage']), $this->server(), 'player.verify', ['player' => 'alice', 'code' => 'x']),
            'ernestdefoe-garrison.api.errors.not_permitted'
        );
    }

    public function testTheLinkerMayStillQueuePlayerVerify(): void
    {
        $this->dispatcher()->permitted($this->actor([]), $this->server(), 'player.verify', Dispatcher::SOURCE_IDENTITY);

        $this->addToAssertionCount(1);
    }

    /**
     * 🚨 The wiring half. The refusal above keys on the source, so it only
     * protects anything if the generic endpoint cannot claim to be the Linker.
     */
    public function testTheGenericEndpointDoesNotChooseItsSource(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Api/Controller/QueueCommandController.php');

        $this->assertStringNotContainsString('SOURCE_IDENTITY', $source);
        $this->assertStringNotContainsString("'identity'", $source);
        $this->assertDoesNotMatchRegularExpression('/\$body\[.source.\]/', $source, 'the browser must not pick the source');
    }
}

/** Exposes the two decisions so the permitted paths can be asserted without a database. */
class ExposedDispatcher extends Dispatcher
{
    public function permitted(User $actor, Server $server, string $verb, string $source = 'user'): void
    {
        $this->assertPermitted($actor, $server, $verb, $source);
    }

    public function bounded(string $verb, array $params): array
    {
        return $this->constrain($verb, $params);
    }
}
