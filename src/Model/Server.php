<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Health\HealthText;
use Flarum\Database\AbstractModel;
use Flarum\Locale\TranslatorInterface;
use Flarum\User\User;
use Illuminate\Database\Eloquent\Builder;
use Illuminate\Database\Eloquent\Relations\BelongsTo;
use Illuminate\Database\Eloquent\Relations\HasMany;

/**
 * The forum's cached view of one game server.
 *
 * @property int $id
 * @property int $agent_id
 * @property string $ref
 * @property string $name
 * @property string $driver
 * @property string $state
 * @property string|null $state_detail
 * @property int|null $pid
 * @property \Carbon\Carbon|null $running_since
 * @property float|null $cpu_percent
 * @property int|null $memory_bytes
 * @property int|null $memory_limit
 * @property string|null $stats_source
 * @property int|null $players_online
 * @property int|null $players_max
 * @property \Carbon\Carbon|null $last_status_at
 * @property bool $is_public
 * @property int|null $join_group_id
 * @property string|null $join_address
 * @property string|null $join_password
 * @property string|null $join_code
 * @property \Carbon\Carbon $created_at
 * @property \Carbon\Carbon|null $updated_at
 * @property string|null $game
 * @property string|null $icon_url
 * @property string|null $health_state
 * @property string|null $health_summary
 * @property string|null $health_checks
 * @property \Carbon\Carbon|null $unready_since
 * @property int $unready_polls
 * @property \Carbon\Carbon|null $last_remediation_at
 * @property bool $needs_attention
 * @property bool $auto_remediate
 * @property int $icon_attempts
 * @property int $backup_every_hours
 * @property \Carbon\Carbon|null $backup_queued_at
 * @property string|null $backups
 * @property bool $offsite_configured
 * @property string|null $offsite_bucket
 * @property \Carbon\Carbon|null $offsite_last_at
 * @property bool $offsite_last_ok
 * @property string|null $offsite_last_error
 * @property string|null $players_online_names
 * @property bool $players_known
 * @property string|null $health_summary_id
 * @property string|null $health_summary_params
 * @property bool|null $players_can_verify
 * @property-read GarrisonAgent|null $agent
 * @property-read \Illuminate\Database\Eloquent\Collection<int, PlaySession> $sessions
 */
class Server extends AbstractModel
{
    protected $table = 'garrison_servers';

    /**
     * Only the servers this edition of Garrison covers.
     *
     * 🚨 Applied at the two places a READER can see servers — the forum's
     * list and the widget — and deliberately NOT applied to the admin panel or
     * to the agent gateway.
     *
     * The gateway must keep recording every server the agent reports, or an
     * operator who installs garrison-pro later gets a history that begins the
     * moment they paid instead of one that was there all along. The admin
     * panel must keep showing every server, or choosing which one the free
     * tier covers would mean choosing from a list that already hides the
     * others — and a server that has quietly vanished from the panel reads as
     * Garrison having lost it.
     *
     * 🚨 Qualified with the table name. A bare `id` is ambiguous the moment
     * anything joins, and it 500s rather than filtering wrongly — which is the
     * better failure, but only if it never ships. `getTable()` also carries
     * the installation's table prefix, which a hand-written string would not.
     */
    /**
     * @param Builder<$this> $query
     * @return Builder<$this>
     */
    public function scopeEntitled(Builder $query): Builder
    {
        $ids = resolve(\ErnestDefoe\Garrison\Entitled::class)->serverIds();

        if ($ids === null) {
            return $query;
        }

        return $query->whereIn($this->getTable().'.id', $ids);
    }

    /**
     * 🚨 EVERY datetime column belongs here, and a missing one is a fatal on
     * whichever code path first calls a Carbon method on it.
     *
     * `last_remediation_at` was added to the schema and not to this list, so
     * it came back as a string and `->gt()` on it killed the scheduled health
     * command with exit 255 and no output. It survived three test runs because
     * the column is NULL until the ladder acts once — the check that reads it
     * is unreachable until then. The same shape as the missing `now()` helper
     * earlier: correct-looking code on a path nothing had exercised yet.
     */
    protected $casts = [
        'is_public' => 'bool',
        'needs_attention' => 'bool',
        'auto_remediate' => 'bool',
        'running_since' => 'datetime',
        'last_status_at' => 'datetime',
        'unready_since' => 'datetime',
        'last_remediation_at' => 'datetime',
        'backup_queued_at' => 'datetime',
        'offsite_configured' => 'bool',
        'offsite_last_ok' => 'bool',
        'offsite_last_at' => 'datetime',
        'players_known' => 'bool',
        'players_can_verify' => 'bool',
        'created_at' => 'datetime',
        'updated_at' => 'datetime',
    ];

    /** @return BelongsTo<GarrisonAgent, $this> */
    public function agent(): BelongsTo
    {
        return $this->belongsTo(GarrisonAgent::class, 'agent_id');
    }

    /**
     * 🚨 Whether $actor may see the address, password and join code.
     *
     * This one method is the whole gate, and it lives on the model precisely
     * so all four widget hosts and the status page ask the SAME question. A
     * gate re-implemented per surface is a gate that is open on one of them.
     */
    public function joinDetailsVisibleTo(?User $actor): bool
    {
        if ($actor === null) {
            return false;
        }

        if ($actor->hasPermission('garrison.manage')) {
            return true;
        }

        // No group set means staff only — the safe default. An operator who
        // wants it wider has to say so, rather than discovering they did.
        if ($this->join_group_id === null) {
            return false;
        }

        /**
         * 🚨 permissionGroupIds(), NOT $actor->groups.
         *
         * `groups` is the explicit pivot table only. Flarum's Members and
         * Guests groups are IMPLICIT — every confirmed account is a Member
         * without a row saying so — so a hand-rolled membership check silently
         * fails for exactly the two groups an operator is most likely to pick.
         *
         * Caught on dev: join_group_id was set to Members and a member still
         * saw nothing, with no error anywhere. That is the shape of a setting
         * that looks wired and does nothing — the commonest bug in this whole
         * codebase's family. permissionGroupIds() is core's own answer and
         * includes the implicit groups plus anything a group processor adds.
         */
        return in_array((int) $this->join_group_id, $actor->permissionGroupIds(), true);
    }

    /**
     * The probes that are currently failing, newest report.
     *
     * Only the failures: a list of thirty passing checks buries the one that
     * matters, and an operator reading this at 3am needs the finding, not an
     * audit of everything that is fine.
     *
     * @return array<int, array{name: string, detail: string}>
     */
    public function failingChecks(): array
    {
        if (empty($this->health_checks)) {
            return [];
        }

        $decoded = json_decode($this->health_checks, true);

        if (! is_array($decoded)) {
            return [];
        }

        $text = self::healthText();
        $failing = [];

        foreach ($decoded as $check) {
            if (! is_array($check) || ! empty($check['ok']) || ! empty($check['skipped'])) {
                continue;
            }

            $failing[] = [
                // The probe's name is the operator's own words from their
                // agent config, so it is shown as written.
                'name' => (string) ($check['name'] ?? 'check'),
                'detail' => $text->detail($check),
                'warn' => ! empty($check['warn']),
            ];
        }

        return $failing;
    }

    /**
     * The one-line health summary, in the forum's language where the agent
     * sent an ID this forum knows, else the agent's own English.
     */
    public function healthSummaryText(): ?string
    {
        $params = null;

        if (! empty($this->health_summary_params)) {
            $decoded = json_decode($this->health_summary_params, true);
            $params = is_array($decoded) ? $decoded : null;
        }

        return self::healthText()->summary($this->health_summary_id, $params, $this->health_summary);
    }

    private static function healthText(): HealthText
    {
        return new HealthText(resolve(TranslatorInterface::class));
    }

    /**
     * Who is in the game right now, or null when this server does not say.
     *
     * 🚨 null and [] are different answers. "Nobody is playing" and "this
     * server does not report players" look identical in an empty list and mean
     * opposite things — one is an empty game, the other is a feature nobody
     * turned on, and showing "0 players" for the second makes an operator think
     * their server is dead.
     *
     * @return array<int, string>|null
     */
    public function playersOnline(): ?array
    {
        if (! $this->players_known) {
            return null;
        }

        $decoded = json_decode((string) $this->players_online_names, true);

        return is_array($decoded) ? array_values(array_filter($decoded, 'is_string')) : [];
    }

    /**
     * Games whose built-in way to speak to a player is a BROADCAST. Used only
     * for an agent too old to report `canVerify` itself.
     */
    public const BROADCAST_ONLY_GAMES = ['terraria'];

    /**
     * Whether a verification code can be whispered to ONE player here.
     *
     * 🚨 A server that can only broadcast must not offer linking at all. The
     * code would go to global chat, so anybody watching could claim a name
     * that is not theirs, read its code off the chat, and confirm it. The
     * agent's own answer wins; for an agent too old to give one, the game
     * decides.
     */
    public function canVerifyPlayers(): bool
    {
        if (! $this->players_known) {
            return false;
        }

        if ($this->players_can_verify !== null) {
            return (bool) $this->players_can_verify;
        }

        return ! in_array(strtolower((string) $this->game), self::BROADCAST_ONLY_GAMES, true);
    }

    /** @return HasMany<PlaySession, $this> */
    public function sessions(): HasMany
    {
        return $this->hasMany(PlaySession::class, 'server_id');
    }

    /**
     * The archives the agent last reported holding, newest first.
     *
     * 🚨 Shaped for the browser here rather than in the controller, so the
     * status page and any future surface get the same list. The cap is the
     * agent's (MaxBackupsShipped); this does not re-cap, because a shorter
     * list here would silently hide the older half of what an operator can
     * actually restore.
     *
     * @return array<int, array{id: string, size: int, at: ?string, safety: bool}>
     */
    public function backupList(): array
    {
        if (empty($this->backups)) {
            return [];
        }

        $decoded = json_decode($this->backups, true);

        if (! is_array($decoded)) {
            return [];
        }

        $out = [];

        foreach ($decoded as $b) {
            if (! is_array($b) || empty($b['id'])) {
                continue;
            }

            $out[] = [
                'id' => (string) $b['id'],
                'size' => (int) ($b['size'] ?? 0),
                'at' => empty($b['at']) ? null : (string) $b['at'],
                'safety' => ! empty($b['safety']),
            ];
        }

        return $out;
    }

    /**
     * Stale means the agent has not reported recently, so what is on screen is
     * a memory rather than a fact. Shown as such: a panel confidently
     * displaying a twenty-minute-old "running" is how an outage goes unnoticed
     * for twenty hours.
     */
    /**
     * 🚨 Carbon::now(), not now(). `now()` is one of Laravel's global helper
     * functions and Flarum does not load them, so an unqualified call resolves
     * against this namespace, finds nothing, and fatals — but only on the code
     * path that calls it. The extension installed, migrated, paired and ran a
     * real agent before this was hit, because nothing reached isStale() until
     * a browser asked for the server list.
     */
    public function isStale(): bool
    {
        if ($this->last_status_at === null) {
            return true;
        }

        return $this->last_status_at->lt(Carbon::now()->subSeconds(90));
    }
}
