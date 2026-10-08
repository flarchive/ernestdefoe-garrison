<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;
use Illuminate\Database\Eloquent\Relations\HasMany;

/**
 * A paired game host.
 *
 * Named GarrisonAgent rather than Agent because `Agent` alone collides with
 * half the user-agent libraries a forum will have in its autoloader, and the
 * resulting "wrong class" errors are miserable to diagnose.
 *
 * @property int $id
 * @property string $name
 * @property string $token_hash
 * @property string|null $version
 * @property string|null $os
 * @property string|null $arch
 * @property string|null $drivers
 * @property \Carbon\Carbon|null $last_seen_at
 * @property \Carbon\Carbon $created_at
 * @property \Carbon\Carbon|null $updated_at
 * @property-read \Illuminate\Database\Eloquent\Collection<int, Server> $servers
 */
class GarrisonAgent extends AbstractModel
{
    protected $table = 'garrison_agents';

    protected $casts = [
        'last_seen_at' => 'datetime',
        'created_at' => 'datetime',
        'updated_at' => 'datetime',
    ];

    /** @return HasMany<Server, $this> */
    public function servers(): HasMany
    {
        return $this->hasMany(Server::class, 'agent_id');
    }

    /**
     * An agent is considered late after two missed polls plus slack. The
     * forum shows this rather than "offline" so a momentary blip during a
     * deploy does not page anybody.
     */
    public function isLate(): bool
    {
        if ($this->last_seen_at === null) {
            return true;
        }

        return $this->last_seen_at->lt(Carbon::now()->subSeconds(90));
    }

    public function driverList(): array
    {
        if (empty($this->drivers)) {
            return [];
        }

        $decoded = json_decode($this->drivers, true);

        return is_array($decoded) ? $decoded : [];
    }
}
