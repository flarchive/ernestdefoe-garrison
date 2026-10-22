<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;
use Illuminate\Database\Eloquent\Builder;
use Illuminate\Database\Eloquent\Relations\BelongsTo;

/**
 * One stretch of time somebody spent in a game.
 *
 * 🚨 `PlaySession`, not `Session`. Flarum and Laravel both have a Session and
 * the collision is not a compile error — it is an import somebody gets wrong
 * once, at three in the morning, and then spends an hour on.
 *
 * @property int $id
 * @property int $server_id
 * @property string $player
 * @property \Carbon\Carbon $started_at
 * @property \Carbon\Carbon|null $ended_at
 * @property int $seconds
 * @property-read Server|null $server
 */
class PlaySession extends AbstractModel
{
    protected $table = 'garrison_sessions';

    /**
     * 🚨 Every datetime, because a missing cast comes back as a string and the
     * first Carbon call on it is a fatal on a path that may not run for days.
     */
    protected $casts = [
        'started_at' => 'datetime',
        'ended_at' => 'datetime',
    ];

    /** @return BelongsTo<Server, $this> */
    public function server(): BelongsTo
    {
        return $this->belongsTo(Server::class, 'server_id');
    }

    /**
     * @param Builder<$this> $query
     * @return Builder<$this>
     */
    public function scopeOpen(Builder $query): Builder
    {
        return $query->whereNull('ended_at');
    }

    /**
     * How long this session ran for, in seconds — including one still open.
     *
     * 🚨 An open session is measured to NOW rather than reported as zero. The
     * person somebody is most likely to look up is the one currently playing,
     * and "0 minutes" next to a green dot is the panel contradicting itself.
     */
    public function seconds(): int
    {
        if ($this->ended_at !== null) {
            return (int) $this->seconds;
        }

        return max(0, Carbon::now()->getTimestamp() - $this->started_at->getTimestamp());
    }
}
