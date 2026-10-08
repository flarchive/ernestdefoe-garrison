<?php

namespace ErnestDefoe\Garrison\Model;

use Flarum\Database\AbstractModel;
use Flarum\User\User;
use Illuminate\Database\Eloquent\Relations\BelongsTo;

/**
 * One queued instruction, and its own audit record.
 *
 * @property int $id
 * @property int $agent_id
 * @property string|null $server_ref
 * @property string $verb
 * @property string|null $params
 * @property string $status
 * @property string|null $result
 * @property string|null $error_code
 * @property string|null $error_message
 * @property int|null $actor_id
 * @property string $source  user | schedule | health
 * @property \Carbon\Carbon $created_at
 * @property \Carbon\Carbon|null $delivered_at
 * @property \Carbon\Carbon|null $completed_at
 * @property-read User|null $actor
 * @property-read GarrisonAgent|null $agent
 */
class Command extends AbstractModel
{
    protected $table = 'garrison_commands';

    protected $casts = [
        'created_at' => 'datetime',
        'delivered_at' => 'datetime',
        'completed_at' => 'datetime',
    ];

    /** @return BelongsTo<User, $this> */
    public function actor(): BelongsTo
    {
        return $this->belongsTo(User::class, 'actor_id');
    }

    /** @return BelongsTo<GarrisonAgent, $this> */
    public function agent(): BelongsTo
    {
        return $this->belongsTo(GarrisonAgent::class, 'agent_id');
    }

    public function paramsArray(): array
    {
        if (empty($this->params)) {
            return [];
        }

        $decoded = json_decode($this->params, true);

        return is_array($decoded) ? $decoded : [];
    }
}
