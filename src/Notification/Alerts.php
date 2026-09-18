<?php

namespace ErnestDefoe\Garrison\Notification;

use ErnestDefoe\Garrison\Model\Server;
use Flarum\Group\Group;
use Flarum\Group\Permission;
use Flarum\Notification\NotificationSyncer;
use Flarum\User\User;
use Psr\Log\LoggerInterface;

/**
 * Sends the alert when a server's health changes, wherever it should go.
 *
 * 🚨 One place, called from the ladder, so a new destination is added once
 * rather than in every branch that might want to tell somebody.
 */
class Alerts
{
    public function __construct(
        protected NotificationSyncer $notifications,
        protected LoggerInterface $log,
        protected Webhooks $webhooks
    ) {
    }

    /**
     * @param string $state down | unready | recovered | abandoned
     */
    public function serverIncident(Server $server, string $state, ?string $summary = null): void
    {
        $recipients = $this->recipients();

        if ($recipients === []) {
            /*
             * 🚨 Worth a line in the log, because it is a real and silent
             * misconfiguration: an incident fired and there is nobody the
             * forum could tell. The commonest cause is `garrison.manage`
             * granted to no group at all — at which point the entire alerting
             * half of this product is off, and the only symptom is that
             * nothing ever arrives, which is indistinguishable from nothing
             * ever going wrong.
             */
            $this->log->warning(
                'Garrison: {server} went {state} and no user has garrison.manage, so nobody was notified.',
                ['server' => $server->name, 'state' => $state]
            );
        } else {
            $this->notifications->sync(
                new ServerIncidentBlueprint($server, $state, $summary),
                $recipients
            );
        }

        $this->webhooks->serverIncident($server, $state, $summary);
    }

    /**
     * Who hears about a server incident.
     *
     * 🚨 Everyone who can manage Garrison, rather than a list somebody has to
     * maintain. A subscriber list is a thing that goes stale the moment
     * somebody leaves, and the failure mode — an outage nobody was told about
     * because the only subscriber left the community last year — is exactly
     * what this feature exists to prevent.
     *
     * 🚨 RESOLVED THROUGH GROUPS, NOT BY SCANNING EVERY USER.
     *
     * The obvious implementation is `User::query()->get()->filter(fn ($u) =>
     * $u->hasPermission(...))`, and it is what this method used to do. It is
     * correct and it does not scale: this runs from a command scheduled EVERY
     * MINUTE, so on a forum with a hundred thousand accounts it hydrates a
     * hundred thousand User models, evaluates a permission gate against each,
     * and does it again sixty seconds later. That is a memory profile that
     * ends in the health checker — the thing watching for outages — being the
     * process the kernel kills.
     *
     * Permissions in Flarum are granted to GROUPS, so the groups are what to
     * ask. The two subtleties, both of which have bitten this codebase before:
     * administrators hold every permission without a row saying so, and the
     * Members group is implicit — no pivot row exists for it — so granting
     * `garrison.manage` to Members means every confirmed account, which has to
     * be spelled out rather than discovered through a join that finds nobody.
     *
     * @return array<int, User>
     */
    protected function recipients(): array
    {
        $groups = Permission::query()
            ->where('permission', 'garrison.manage')
            ->pluck('group_id')
            ->map(fn ($id) => (int) $id)
            ->all();

        // Administrators are never in this table for any permission.
        $groups[] = Group::ADMINISTRATOR_ID;

        $query = User::query();

        if (in_array(Group::MEMBER_ID, $groups, true)) {
            // Granted to Members: every registered account, and no join would
            // have found them, because nothing writes that membership down.
            return $query->get()->all();
        }

        // Guests cannot receive a notification — there is no account to put it
        // in — so the grant is meaningless here and dropping it keeps the
        // query honest rather than silently matching nothing.
        $groups = array_values(array_diff(array_unique($groups), [Group::GUEST_ID]));

        /*
         * 🚨 Through the relationship, and the column qualified from the
         * model's own table name — NOT a hand-written `exists (select 1 from
         * group_user ...)`.
         *
         * This forum runs with a table prefix (`dev_`) for exactly this class
         * of bug, and prefixes are the reason: Laravel's grammar applies the
         * connection prefix when it WRAPS a table, so every table name that
         * reaches the query through the builder is rewritten, and every table
         * name written into a raw string is not. A raw subquery here would
         * work perfectly on an unprefixed install and fail with "table
         * garrison.group_user doesn't exist" on a customer's — which is the
         * half of the world nobody develops against.
         */
        return $query
            ->whereHas('groups', function ($q) use ($groups) {
                $q->whereIn($q->getModel()->getTable() . '.id', $groups);
            })
            ->get()
            ->all();
    }
}
