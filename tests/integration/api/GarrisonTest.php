<?php

namespace ErnestDefoe\Garrison\Tests\integration\api;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Agent\TokenGuard;
use Flarum\Group\Group;
use Flarum\Testing\integration\RetrievesAuthorizedUsers;
use Flarum\Testing\integration\TestCase;
use Flarum\User\User;
use PHPUnit\Framework\Attributes\Test;

/**
 * Users: 1 admin · 2 a member · 3 an operator (view and control) · 4 in the
 * Players group (5), which may see server 1's join details.
 * One agent; server 1 public, server 2 private.
 */
class GarrisonTest extends TestCase
{
    use RetrievesAuthorizedUsers;

    private string $token;

    protected function setUp(): void
    {
        parent::setUp();

        $this->extension('ernestdefoe-garrison');

        [$this->token, $hash] = TokenGuard::mint(1);
        $now = Carbon::now();
        $server = fn (int $id, string $name, bool $public, array $extra = []) => $extra + [
            'id' => $id, 'agent_id' => 1, 'ref' => "srv$id", 'name' => $name, 'driver' => 'process', 'state' => 'running',
            'is_public' => $public, 'join_address' => "play$id.example:2456", 'join_password' => "secret$id", 'created_at' => $now,
        ];

        $this->prepareDatabase([
            User::class => [
                $this->normalUser(),
                ['id' => 3, 'username' => 'operator', 'email' => 'op@machine.local', 'password' => 'too-obscure', 'is_email_confirmed' => 1],
                ['id' => 4, 'username' => 'player', 'email' => 'player@machine.local', 'password' => 'too-obscure', 'is_email_confirmed' => 1],
                ['id' => 5, 'username' => 'watcher', 'email' => 'watcher@machine.local', 'password' => 'too-obscure', 'is_email_confirmed' => 1],
            ],
            Group::class => [
                ['id' => 5, 'name_singular' => 'Player', 'name_plural' => 'Players', 'is_hidden' => 0],
                ['id' => 6, 'name_singular' => 'Operator', 'name_plural' => 'Operators', 'is_hidden' => 0],
                ['id' => 7, 'name_singular' => 'Watcher', 'name_plural' => 'Watchers', 'is_hidden' => 0],
            ],
            'group_user' => [['user_id' => 4, 'group_id' => 5], ['user_id' => 3, 'group_id' => 6], ['user_id' => 5, 'group_id' => 7]],
            'group_permission' => [
                ['group_id' => 6, 'permission' => 'garrison.view'], ['group_id' => 6, 'permission' => 'garrison.control'],
                ['group_id' => 7, 'permission' => 'garrison.view'],
            ],
            'garrison_agents' => [['id' => 1, 'name' => 'Box', 'token_hash' => $hash, 'last_seen_at' => $now, 'created_at' => $now]],
            'garrison_servers' => [$server(1, 'Valheim', true, ['join_group_id' => 5]), $server(2, 'Staff MC', false)],
            'garrison_console' => [['id' => 1, 'agent_id' => 1, 'server_ref' => 'srv1', 'at' => $now, 'text' => '[chat] someone: hi', 'stderr' => false]],
        ]);
    }

    private function call(string $method, string $path, ?int $actor, array $body = [], array $headers = []): array
    {
        $request = $this->request($method, $path, ($actor ? ['authenticatedAs' => $actor] : []) + ($body ? ['json' => $body] : []));
        foreach ($headers as $name => $value) {
            $request = $request->withHeader($name, $value);
        }
        $response = $this->send($request);

        return [$response->getStatusCode(), json_decode((string) $response->getBody(), true)];
    }

    /** @return array<string, array<string, mixed>> servers by name */
    private function servers(?int $actor): array
    {
        [$status, $body] = $this->call('GET', '/api/garrison/servers', $actor);
        $this->assertSame(200, $status);

        return array_column($body['data'] ?? $body['servers'] ?? $body, null, 'name');
    }

    #[Test]
    public function the_free_edition_lists_one_server()
    {
        $this->assertSame(['Valheim'], array_keys($this->servers(1)), 'The first, unless another is chosen');
    }

    #[Test]
    public function a_private_server_is_listed_only_to_operators()
    {
        // The free edition's one server is the private one.
        $this->setting('ernestdefoe-garrison.entitled_server_id', '2');

        $this->assertSame([], $this->servers(null));
        $this->assertSame([], $this->servers(2));
        $this->assertSame(['Staff MC'], array_keys($this->servers(3)));
        $this->assertArrayNotHasKey('joinPassword', $this->servers(3)['Staff MC'], 'No group chosen means staff only, and operators are not staff');
    }

    #[Test]
    public function the_way_in_is_given_only_to_the_chosen_group_and_staff()
    {
        $this->assertArrayNotHasKey('joinPassword', $this->servers(null)['Valheim']);
        $this->assertArrayNotHasKey('joinPassword', $this->servers(2)['Valheim']);
        $this->assertSame('secret1', $this->servers(4)['Valheim']['joinPassword'], 'The Players group');
        $this->assertSame('secret1', $this->servers(1)['Valheim']['joinPassword'], 'An admin');
    }

    #[Test]
    public function operational_detail_is_for_operators()
    {
        $this->assertArrayNotHasKey('backups', $this->servers(2)['Valheim']);
        $this->assertArrayHasKey('backups', $this->servers(3)['Valheim']);
        $this->assertTrue($this->servers(3)['Valheim']['canControl']);
        $this->assertFalse($this->servers(3)['Valheim']['canConsole']);
    }

    #[Test]
    public function commands_are_queued_only_by_those_permitted()
    {
        $queue = fn (?int $actor, int $server, string $verb) => $this->call('POST', "/api/garrison/servers/$server/command", $actor, ['verb' => $verb]);

        $this->assertSame(422, $queue(2, 1, 'server.restart')[0], 'A member');
        $this->assertSame(422, $queue(5, 1, 'server.restart')[0], 'Viewing is not control');
        $this->assertSame(422, $queue(3, 1, 'backup.delete')[0], 'Destructive needs manage');
        $this->assertSame(422, $queue(3, 1, 'console.send')[0], 'The console needs its own permission');
        $this->assertSame(422, $queue(3, 1, 'rm -rf')[0], 'Not a verb');
        $this->assertSame(0, $this->database()->table('garrison_commands')->count());

        [$status, $body] = $queue(3, 1, 'server.restart');
        $this->assertSame(202, $status);
        $this->assertSame('queued', $body['data']['status']);
        $this->assertSame(404, $queue(3, 99, 'server.restart')[0]);
    }

    #[Test]
    public function destroying_a_backup_needs_manage_even_in_the_pro_edition()
    {
        \ErnestDefoe\Garrison\Edition::enablePro();

        $this->assertSame(422, $this->call('POST', '/api/garrison/servers/1/command', 3, ['verb' => 'backup.delete'])[0], 'Control is not enough');
        $this->assertSame(202, $this->call('POST', '/api/garrison/servers/1/command', 1, ['verb' => 'backup.delete'])[0]);
    }

    #[Test]
    public function the_free_edition_runs_commands_on_one_server()
    {
        $this->assertSame(422, $this->call('POST', '/api/garrison/servers/2/command', 1, ['verb' => 'server.restart'])[0], 'Not even an admin, on the second server');
        $this->assertSame(202, $this->call('POST', '/api/garrison/servers/1/command', 1, ['verb' => 'server.restart'])[0]);
    }

    #[Test]
    public function a_command_is_followed_only_by_whoever_queued_it_or_a_manager()
    {
        [, $body] = $this->call('POST', '/api/garrison/servers/1/command', 3, ['verb' => 'server.restart']);
        $id = $body['data']['id'];

        $this->assertSame(200, $this->call('GET', "/api/garrison/commands/$id", 3)[0]);
        $this->assertSame(404, $this->call('GET', "/api/garrison/commands/$id", 2)[0]);
        $this->assertSame(200, $this->call('GET', "/api/garrison/commands/$id", 1)[0]);
    }

    #[Test]
    public function the_console_is_for_console_permission_holders()
    {
        $this->assertSame(403, $this->call('GET', '/api/garrison/servers/1/console', 3)[0], 'View and control are not the console');
        $this->assertSame(403, $this->call('GET', '/api/garrison/servers/1/console', null)[0]);

        [$status, $body] = $this->call('GET', '/api/garrison/servers/1/console', 1);
        $this->assertSame(200, $status);
        $this->assertSame(['[chat] someone: hi'], array_column($body['lines'], 'text'));
    }

    #[Test]
    public function the_admin_panel_is_for_managers()
    {
        $this->assertSame(403, $this->call('GET', '/api/garrison/admin/state', 3)[0]);
        $this->assertSame(403, $this->call('DELETE', '/api/garrison/admin/agents/1', 3)[0]);
        $this->assertSame(200, $this->call('GET', '/api/garrison/admin/state', 1)[0]);
    }

    #[Test]
    public function only_a_paired_agent_with_its_secret_can_report()
    {
        $poll = fn (?string $token) => $this->call('POST', '/api/garrison/agent/poll', null, ['servers' => [['server' => 'srv1', 'state' => 'stopped']]], $token === null ? [] : ['Authorization' => "Bearer $token"]);

        $this->assertSame(401, $poll(null)[0]);
        $this->assertSame(401, $poll('1.wrong')[0]);
        $this->assertSame(401, $poll('2.'.substr($this->token, 2))[0], 'The secret of another agent id');
        $this->assertSame('running', $this->database()->table('garrison_servers')->where('id', 1)->value('state'));

        // A queued command, so the poll answers at once instead of waiting
        // out its long-poll window.
        $this->database()->table('garrison_commands')->insert(['id' => 7, 'agent_id' => 1, 'server_ref' => 'srv1', 'verb' => 'server.start', 'status' => 'queued', 'created_at' => Carbon::now()]);

        [$status, $body] = $poll($this->token);
        $this->assertSame(200, $status);
        $this->assertSame('stopped', $this->database()->table('garrison_servers')->where('id', 1)->value('state'));
        $this->assertStringContainsString('server.start', json_encode($body), 'The agent receives its work');
        $this->assertSame('delivered', $this->database()->table('garrison_commands')->where('id', 7)->value('status'));
    }
}
