<?php

namespace ErnestDefoe\Garrison\Model;

use Flarum\Database\AbstractModel;
use Flarum\User\User;

/**
 * One queued instruction, and its own audit record.
 */
class Command extends AbstractModel
{
    protected $table = 'garrison_commands';

    protected $casts = [
        'created_at' => 'datetime',
        'delivered_at' => 'datetime',
        'completed_at' => 'datetime',
    ];

    public function actor()
    {
        return $this->belongsTo(User::class, 'actor_id');
    }

    public function agent()
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
