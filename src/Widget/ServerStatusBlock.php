<?php

namespace ErnestDefoe\Garrison\Widget;

use ErnestDefoe\Garrison\Model\Server;
use Ernestdefoe\PageBuilder\Block\BlockInterface;
use Flarum\User\User;

/**
 * Garrison's server-status widget, as a Page Builder block.
 *
 * 🚨 Page Builder is the only one of the four widget hosts whose contract has
 * a SERVER half, and that turns out to be the useful one rather than the
 * awkward one: resolve() is where "may this actor see the join code" belongs.
 * The other three hosts (stock sidebar, fof/forum-widgets-core, Bespoke) are
 * client-only and read the same resolved snapshot through the API, so the gate
 * is written once and cannot be open on one surface and shut on another.
 *
 * 🚨 This class is referenced from extend.php only when Page Builder is
 * installed, but it must EXIST unconditionally — a class_exists guard around
 * the extender does not save a forum from a fatal if the class it names is
 * missing.
 */
class ServerStatusBlock implements BlockInterface
{
    public function type(): string
    {
        return 'garrison-server-status';
    }

    public function name(): string
    {
        return 'Game servers';
    }

    public function icon(): string
    {
        return 'fas fa-tower-observation';
    }

    public function category(): string
    {
        return 'forum';
    }

    public function defaultSettings(): array
    {
        return [
            'servers' => 'public',
            'showPlayers' => true,
            'showJoin' => true,
            'compact' => false,
        ];
    }

    public function settingsSchema(): array
    {
        return [
            [
                'key' => 'servers',
                'type' => 'select',
                'label' => 'Which servers',
                'default' => 'public',
                'options' => [
                    ['value' => 'public', 'label' => 'Every public server'],
                    ['value' => 'running', 'label' => 'Only servers that are up'],
                ],
            ],
            [
                'key' => 'showPlayers',
                'type' => 'toggle',
                'label' => 'Show who is online',
                'default' => true,
            ],
            [
                'key' => 'showJoin',
                'type' => 'toggle',
                'label' => 'Show join details',
                'default' => true,
                // Says what the toggle actually does, because "show join
                // details" reads like it overrides the permission and it does
                // not — nor should a layout choice be able to.
                'help' => 'Only ever shown to people whose group is allowed to see them.',
            ],
            [
                'key' => 'compact',
                'type' => 'toggle',
                'label' => 'Compact rows',
                'default' => false,
            ],
        ];
    }

    /**
     * 🚨 Scoped to $actor, every time, and never trusting the settings to do
     * it. `showJoin` decides whether this block WANTS to show join details;
     * joinDetailsVisibleTo decides whether this person may see them. A block
     * setting is a layout choice made by whoever edited the page, and layout
     * choices must not be able to widen who can see an address and password.
     */
    public function resolve(array $settings, User $actor): array
    {
        $query = Server::query()->entitled()->where('is_public', true);

        if (($settings['servers'] ?? 'public') === 'running') {
            $query->where('state', 'running');
        }

        $servers = $query->orderBy('name')->get();

        return [
            'servers' => $servers->map(function (Server $server) use ($settings, $actor) {
                $row = [
                    'id' => $server->id,
                    'name' => $server->name,
                    'state' => $server->state,
                    'stale' => $server->isStale(),
                ];

                if ($settings['showPlayers'] ?? true) {
                    $row['playersOnline'] = $server->players_online;
                    $row['playersMax'] = $server->players_max;
                }

                if (($settings['showJoin'] ?? true) && $server->joinDetailsVisibleTo($actor)) {
                    $row['joinAddress'] = $server->join_address;
                    $row['joinPassword'] = $server->join_password;
                    $row['joinCode'] = $server->join_code;
                }

                return $row;
            })->all(),
        ];
    }
}
