<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;
use Flarum\User\User;
use Illuminate\Database\Eloquent\Builder;
use Illuminate\Database\Eloquent\Relations\BelongsTo;

/**
 * A forum account and the in-game player it belongs to.
 *
 * @property int $id
 * @property int $user_id
 * @property int $server_id
 * @property string $player
 * @property \Carbon\Carbon|null $verified_at
 * @property string|null $code_hash
 * @property \Carbon\Carbon|null $code_expires_at
 * @property int $attempts
 * @property \Carbon\Carbon|null $created_at
 * @property-read User|null $user
 * @property-read Server|null $server
 */
class Identity extends AbstractModel
{
    protected $table = 'garrison_identities';

    /**
     * 🚨 Every datetime. A missing cast comes back as a string and the first
     * Carbon call on it is a fatal — on a path that may not run for days.
     */
    protected $casts = [
        'verified_at' => 'datetime',
        'code_expires_at' => 'datetime',
        'created_at' => 'datetime',
    ];

    /**
     * 🚨 Never serialised anywhere. The hash is an implementation detail of
     * verification and has no business being in an API payload, so it is
     * hidden at the model rather than remembered at each of the places that
     * might return one.
     */
    protected $hidden = ['code_hash'];

    /** @return BelongsTo<User, $this> */
    public function user(): BelongsTo
    {
        return $this->belongsTo(User::class, 'user_id');
    }

    /** @return BelongsTo<Server, $this> */
    public function server(): BelongsTo
    {
        return $this->belongsTo(Server::class, 'server_id');
    }

    /**
     * @param Builder<$this> $query
     * @return Builder<$this>
     */
    public function scopeVerified(Builder $query): Builder
    {
        return $query->whereNotNull('verified_at');
    }

    public function isVerified(): bool
    {
        return $this->verified_at !== null;
    }

    /**
     * How long a code is worth typing.
     *
     * 🚨 Ten minutes, which is long enough to alt-tab and short enough that a
     * code left on screen in a shared house is not a standing invitation. The
     * whole flow is "read this in the game, type it here" — anybody who needs
     * longer than ten minutes has walked away, and asking for a new code costs
     * one click.
     */
    public const CODE_TTL_MINUTES = 10;

    /**
     * 🚨 Five, then the claim is dead and a new code is needed.
     *
     * Six characters from an unambiguous alphabet is about a billion
     * possibilities, so guessing is not the threat — but a cap turns an
     * automated attempt from "slow" into "pointless", and it also stops a
     * confused person burning through a code they mistyped once.
     */
    public const MAX_ATTEMPTS = 5;

    public function codeIsLive(): bool
    {
        return $this->code_hash !== null
            && $this->code_expires_at !== null
            && $this->code_expires_at->gt(Carbon::now())
            && $this->attempts < self::MAX_ATTEMPTS;
    }
}
