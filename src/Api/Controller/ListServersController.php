<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use ErnestDefoe\Garrison\Game\Marks;
use ErnestDefoe\Garrison\Model\Identity;
use ErnestDefoe\Garrison\Game\Catalog;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Http\RequestUtil;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * The one endpoint every surface reads.
 *
 * 🚨 ONE query, one response, for the whole page — whatever is on it. The
 * status page, the sidebar widget, a Bespoke widget and a Page Builder block
 * can all be on screen together, and they must cost one request between them,
 * not one each. A request per rendered item is not a theoretical worry here:
 * it exhausted a database connection cap and 500'd a whole forum once already.
 */
class ListServersController implements RequestHandlerInterface
{
    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $actor = RequestUtil::getActor($request);

        $query = Server::query()->entitled()->with('agent')->orderBy('name');

        /**
         * 🚨 `is_public` on the server is the ONLY thing that decides whether
         * a server can be seen to exist — by anybody, guests included. There
         * is deliberately no second permission gating the list.
         *
         * Two switches for one outcome is how a control ends up doing nothing:
         * an operator ticks "public" on a server, sees no change because a
         * permission they never heard of is unset, and concludes the feature
         * is broken. One switch, in the place they were already looking.
         *
         * `garrison.view` therefore means something narrower and honest: see
         * the servers that are NOT public. Staff.
         *
         * Filtered in the QUERY, not in the view — a private server is then
         * never in the payload at all, so no later template bug can leak one.
         */
        if (! $actor->hasPermission('garrison.view')) {
            $query->where('is_public', true);
        }

        /*
         * 🚨 One query for every identity, not one per server.
         *
         * A forum with a dozen servers would otherwise make a dozen queries on
         * every poll of every visitor — which is precisely the shape that once
         * exhausted a database connection cap and 500'd a whole forum.
         */
        $identities = $actor->isGuest()
            ? collect()
            : Identity::query()->where('user_id', $actor->id)->get()->keyBy('server_id');

        $servers = $query->get()->map(function (Server $server) use ($actor, $identities) {
            $row = [
                'id' => $server->id,
                'name' => $server->name,
                'driver' => $server->driver,
                'state' => $server->state,
                'detail' => $server->state_detail,
                'stale' => $server->isStale(),
                'playersOnline' => $server->players_online,

                /**
                 * 🚨 The NAMES, where the server reports them, and null where
                 * it does not.
                 *
                 * This is the thing a forum can do that a standalone game
                 * panel cannot: the people in the list are, often, the people
                 * reading the page. Seeing "alice, bob and two others are on
                 * right now" is a reason to go and join them, which is the
                 * whole argument for a game panel living in a community rather
                 * than beside one.
                 *
                 * Public, like the player COUNT already is. A name somebody
                 * chose to display in a shared game is not a secret, and
                 * hiding it while showing "3 players online" would be a
                 * strange half-measure.
                 */
                'playersNames' => $server->playersOnline(),
                'playersMax' => $server->players_max,
                'runningSince' => $server->running_since?->toIso8601String(),
                'lastStatusAt' => $server->last_status_at?->toIso8601String(),
                'agentLate' => $server->agent?->isLate() ?? true,

                // 🚨 Resolved server-side, once. Every widget host and the
                // status page then draw the same thing without each
                // re-implementing the "custom, else mark, else monogram"
                // ladder — which is how three surfaces end up disagreeing
                // about what a server looks like.
                /**
                 * 🚨 Health is a SEPARATE field from state, and that is the
                 * product's whole thesis in one line of JSON. "running" and
                 * "players can actually get in" are different facts; conflating
                 * them is what let a server sit unjoinable for twenty hours
                 * while every dashboard showed green.
                 */
                'health' => $server->health_state,
                'healthSummary' => $server->health_summary,
                'needsAttention' => (bool) $server->needs_attention,
                'autoRemediate' => (bool) $server->auto_remediate,

                'game' => $server->game,

                /*
                 * 🚨 The usual port for this GAME, not a claim about this
                 * server. It fills the silence for a reader who wants to join
                 * and whose operator never filled in a join address — the
                 * commonest state a server page is in, because setting one is
                 * optional and easy to skip.
                 *
                 * `joinAddress` always wins where it exists; this is only ever
                 * shown as "usually port N".
                 */
                'defaultPort' => Catalog::port($server->game),
                'iconUrl' => $server->icon_url,
                'mark' => Marks::forGame($server->game) ?? Marks::FALLBACK,
                'monogram' => Marks::monogram($server->name),
            ];

            // 🚨 Stats only where the source says they are the kernel's own
            // accounting. `ps` numbers double-count shared pages across a
            // process tree, and a figure presented as exact when it is an
            // estimate is worse than no figure — somebody will size a host
            // from it.
            if ($server->cpu_percent !== null) {
                $row['cpuPercent'] = round($server->cpu_percent, 2);
                $row['memoryBytes'] = $server->memory_bytes;
                $row['memoryLimit'] = $server->memory_limit;
                $row['statsSource'] = $server->stats_source;
                $row['statsApproximate'] = $server->stats_source === 'ps';
            }

            /**
             * 🚨 Probe DETAIL is staff-only. A failing check says things like
             * "UDP 2457 has 9600 bytes queued" — port numbers, log patterns,
             * internal addresses. Useful to an operator, and reconnaissance to
             * anybody else. The health STATE is public; the reasons are not.
             */
            if ($actor->hasPermission('garrison.view') || $actor->hasPermission('garrison.manage')) {
                $row['healthChecks'] = $server->failingChecks();

                /**
                 * 🚨 The list rides along here rather than being asked for,
                 * because of WHEN it gets asked for.
                 *
                 * "Is there anything to restore?" is a question somebody asks
                 * in the minute after something went badly wrong. A panel that
                 * answers it by queueing a backup.list verb shows a spinner for
                 * up to a poll window first — and shows nothing at all if the
                 * host has just gone offline, which is one of the reasons
                 * somebody would be asking. The agent ships the list with every
                 * status report so this row is always already true.
                 *
                 * Staff only, with the failing probes, for the same reason:
                 * filenames name the server and say how often it is backed up,
                 * which is operational detail rather than something a player
                 * needs.
                 */
                $row['backups'] = $server->backupList();

                /**
                 * 🚨 The OUTCOME only. Never a key, never a secret, never an
                 * endpoint that embeds one — the bucket name and whether the
                 * last copy worked is the whole of what a panel needs, and
                 * everything beyond that is authority the forum deliberately
                 * does not hold.
                 */
                $row['offsite'] = [
                    'configured' => (bool) $server->offsite_configured,
                    'bucket' => $server->offsite_bucket,
                    'lastAt' => $server->offsite_last_at?->toIso8601String(),
                    'lastOk' => (bool) $server->offsite_last_ok,
                    'lastError' => $server->offsite_last_error,
                ];
            }

            if ($server->joinDetailsVisibleTo($actor)) {
                $row['joinAddress'] = $server->join_address;
                $row['joinPassword'] = $server->join_password;
                $row['joinCode'] = $server->join_code;
            }

            $row['canControl'] = $actor->hasPermission('garrison.control') || $actor->hasPermission('garrison.manage');
            $row['canConsole'] = $actor->hasPermission('garrison.console') || $actor->hasPermission('garrison.manage');

            /**
             * 🚨 Sent so the browser can decide what to DRAW. It is not what
             * decides what is allowed — Dispatcher::assertPermitted does that,
             * on every queue, server-side, and it refuses a restore from
             * anybody without manage no matter what this flag said.
             *
             * Two checks of the same rule, deliberately: the server's is the
             * gate, and this one is so a member who cannot restore is never
             * shown a Restore button that exists only to tell them no.
             */
            $row['canManage'] = $actor->hasPermission('garrison.manage');
            $row['canConfig'] = $actor->hasPermission('garrison.config') || $actor->hasPermission('garrison.manage');

            /**
             * 🚨 The actor's OWN identity on this server, and only ever their
             * own. Sending anybody else's would turn the status page into a
             * directory mapping forum accounts to in-game names, which is a
             * thing somebody may have chosen to link privately.
             *
             * `canLink` is false where the server cannot whisper — a flow that
             * offers to send a code no game will deliver is a button that
             * always fails.
             */
            if (! $actor->isGuest()) {
                $identity = $identities->get($server->id);

                $row['identity'] = $identity === null ? null : [
                    'player' => $identity->player,
                    'verified' => $identity->isVerified(),
                    'awaitingCode' => ! $identity->isVerified() && $identity->codeIsLive(),
                ];

                $row['canLink'] = $server->players_known;
            }

            return $row;
        })->values()->all();

        return new JsonResponse(['data' => $servers]);
    }
}
