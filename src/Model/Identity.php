<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;
use Flarum\User\User;

/**
 * A forum account and the in-game player it belongs to.
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

    public function user()
    {
        return $this->belongsTo(User::class, 'user_id');
    }

    public function server()
    {
        return $this->belongsTo(Server::class, 'server_id');
    }

    public function scopeVerified($query)
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
