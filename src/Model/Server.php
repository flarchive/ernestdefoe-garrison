<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;
use Flarum\User\User;

/**
 * The forum's cached view of one game server.
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
    public function scopeEntitled($query)
    {
        $ids = resolve(\ErnestDefoe\Garrison\Entitled::class)->serverIds();

        if ($ids === null) {
            return $query;
        }

        return $query->whereIn($this->getTable() . '.id', $ids);
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
        'created_at' => 'datetime',
        'updated_at' => 'datetime',
    ];

    public function agent()
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

        $failing = [];

        foreach ($decoded as $check) {
            if (! is_array($check) || ! empty($check['ok']) || ! empty($check['skipped'])) {
                continue;
            }

            $failing[] = [
                'name' => (string) ($check['name'] ?? 'check'),
                'detail' => (string) ($check['detail'] ?? ''),
                'warn' => ! empty($check['warn']),
            ];
        }

        return $failing;
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

    public function sessions()
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
