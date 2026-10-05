<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Agent\TokenGuard;
use ErnestDefoe\Garrison\Game\Artwork;
use ErnestDefoe\Garrison\Game\Catalog;
use ErnestDefoe\Garrison\Model\GarrisonAgent;
use ErnestDefoe\Garrison\Model\Identity;
use ErnestDefoe\Garrison\Model\Incident;
use ErnestDefoe\Garrison\Model\Schedule;
use ErnestDefoe\Garrison\Model\Server;
use ErnestDefoe\Garrison\Health\Heartbeat;
use Flarum\Foundation\ValidationException;
use Flarum\Http\RequestUtil;
use Flarum\Locale\TranslatorInterface;
use Illuminate\Contracts\Filesystem\Factory;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Message\UploadedFileInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * Everything the admin screen needs, behind one permission.
 *
 * 🚨 Every method starts by asserting `garrison.manage`, and none of them take
 * the permission as a parameter or infer it from the route. A controller that
 * checks in some branches and not others is the shape this class exists to
 * avoid — and it manages pairing tokens, which are the keys to every game host
 * a forum controls.
 */
class AdminController implements RequestHandlerInterface
{
    public function __construct(
        protected TranslatorInterface $translator,
        protected Factory $filesystem,
        protected Artwork $artwork,
        protected Heartbeat $heartbeat
    ) {
    }

    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $actor = RequestUtil::getActor($request);
        $actor->assertPermission($actor->hasPermission('garrison.manage'));

        $action = (string) ($request->getAttribute('routeName') ?? '');

        return match ($action) {
            'garrison.admin.state' => $this->state(),
            'garrison.admin.pair' => $this->pair($request),
            'garrison.admin.unpair' => $this->unpair($request),
            'garrison.admin.server' => $this->updateServer($request),
            'garrison.admin.icon' => $this->uploadIcon($request),
            'garrison.admin.fetchIcon' => $this->fetchIcon($request),
            default => new JsonResponse(['errors' => [['code' => 'not_found']]], 404),
        };
    }

    /** Everything on one request, for the same reason the forum side does. */
    protected function state(): ResponseInterface
    {
        return new JsonResponse([
            'agents' => GarrisonAgent::query()->orderBy('name')->get()->map(fn (GarrisonAgent $a) => [
                'id' => $a->id,
                'name' => $a->name,
                'version' => $a->version,
                'os' => $a->os,
                'arch' => $a->arch,
                'drivers' => $a->driverList(),
                'lastSeenAt' => $a->last_seen_at?->toIso8601String(),
                'late' => $a->isLate(),
                'servers' => $a->servers()->count(),
            ])->values()->all(),

            'servers' => Server::query()->orderBy('name')->get()->map(fn (Server $s) => [
                'id' => $s->id,
                'agentId' => $s->agent_id,
                'ref' => $s->ref,
                'name' => $s->name,
                'game' => $s->game,
                'driver' => $s->driver,
                'state' => $s->state,
                'health' => $s->health_state,
                'isPublic' => (bool) $s->is_public,
                'autoRemediate' => (bool) $s->auto_remediate,
                'needsAttention' => (bool) $s->needs_attention,
                'joinGroupId' => $s->join_group_id,
                'joinAddress' => $s->join_address,
                'joinPassword' => $s->join_password,
                'joinCode' => $s->join_code,
                'iconUrl' => $s->icon_url,
                'backupEveryHours' => (int) $s->backup_every_hours,
                'gameName' => Catalog::name($s->game),
                'canFetchLogo' => Catalog::artworkCandidates($s->game) !== [],
            ])->values()->all(),

            /*
             * 🚨 Whether Garrison's machinery is running at all, measured
             * rather than assumed. See Heartbeat: a forum with no scheduler
             * runs none of this and looks entirely normal, and a forum with no
             * queue worker loses every notification in total silence. The
             * panel would otherwise stay green through the outage this product
             * exists to catch.
             */
            'health' => $this->heartbeat->report(),

            /*
             * 🚨 Still listed here, on a free install too, and deliberately.
             *
             * Creating and editing scheduled work lives in garrison-pro; the
             * rows themselves belong to the operator. Reporting them from the
             * one admin endpoint means that removing pro leaves the schedules
             * visible and plainly paused rather than vanished — a nightly
             * restart that silently stopped existing is exactly the invisible
             * failure this product exists to catch. On a forum that has never
             * had pro this is simply an empty array.
             */
            'schedules' => Schedule::query()->orderBy('server_id')->orderBy('at_minute')->get()
                ->map(fn (Schedule $s) => $s->toAdminArray())->values()->all(),

            /*
             * 🚨 Who is linked to whom, because somebody has to be able to
             * undo it.
             *
             * A member can unlink their OWN character, and that covers the
             * honest cases. What it does not cover is the one an operator
             * actually gets asked about: somebody who linked a character,
             * left the community, and whose name the next player now has —
             * or a link made in error by somebody who has since lost their
             * forum account. Without this, the answer to "can you unlink
             * that?" is no, and the only fix is the database.
             *
             * Unverified claims are included and marked. They are pending
             * rather than wrong, and an operator looking at this list is
             * usually trying to work out why somebody's link did not take.
             */
            'identities' => Identity::query()->with('user')->orderBy('server_id')->get()
                ->map(fn (Identity $i) => [
                    'id' => $i->id,
                    'serverId' => $i->server_id,
                    'player' => $i->player,
                    'verified' => $i->isVerified(),
                    'userId' => $i->user?->id,
                    'username' => $i->user?->username,
                    'displayName' => $i->user?->display_name,
                    'since' => $i->verified_at?->toIso8601String(),
                ])->values()->all(),

            'incidents' => Incident::query()->latest('id')->limit(25)->get()->map(fn (Incident $i) => [
                'id' => $i->id,
                'serverId' => $i->server_id,
                'status' => $i->status,
                'cause' => $i->cause,
                'restarts' => $i->restarts,
                'startedAt' => $i->started_at?->toIso8601String(),
                'resolvedAt' => $i->resolved_at?->toIso8601String(),
                'actions' => $i->actionList(),
            ])->values()->all(),
        ]);
    }

    /**
     * Pair a host: create the agent and mint its token.
     *
     * 🚨 The plaintext token is returned EXACTLY ONCE, here, and stored
     * nowhere. The screen has to say so, because an operator who assumes they
     * can come back for it later will close the dialog and have to re-pair.
     */
    protected function pair(ServerRequestInterface $request): ResponseInterface
    {
        $name = trim((string) (($request->getParsedBody() ?? [])['name'] ?? ''));

        if ($name === '') {
            throw new ValidationException([
                'name' => $this->translator->trans('ernestdefoe-garrison.api.errors.name_required'),
            ]);
        }

        $agent = new GarrisonAgent();
        $agent->name = $name;
        $agent->token_hash = '';
        $agent->created_at = Carbon::now();
        $agent->save();

        // The token embeds the row id, so it cannot be minted until the row
        // exists. Saving twice is the honest cost; guessing the next id would
        // race anybody else pairing at the same moment.
        [$plaintext, $hash] = TokenGuard::mint($agent->id);
        $agent->token_hash = $hash;
        $agent->save();

        return new JsonResponse(['id' => $agent->id, 'name' => $agent->name, 'token' => $plaintext], 201);
    }

    /**
     * 🚨 Unpairing deletes the agent's SERVERS from the forum's cache too, and
     * says so on screen. Leaving them behind would show an operator a list of
     * servers that can never update again and that no button can affect — the
     * worst kind of stale, because it looks live.
     */
    protected function unpair(ServerRequestInterface $request): ResponseInterface
    {
        $id = (int) ($request->getQueryParams()['id'] ?? 0);
        $agent = GarrisonAgent::query()->find($id);

        if ($agent === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        Server::query()->where('agent_id', $agent->id)->delete();
        $agent->delete();

        return new JsonResponse(['ok' => true]);
    }

          /**
     * Server settings an operator owns — as opposed to the ones the agent
     * reports, which are never editable here.
     */
    protected function updateServer(ServerRequestInterface $request): ResponseInterface
    {
        $server = Server::query()->find((int) ($request->getQueryParams()['id'] ?? 0));

        if ($server === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        $body = (array) ($request->getParsedBody() ?? []);

        /**
         * 🚨 An allowlist, not a mass assign. `state`, `health_state`, `ref`
         * and `agent_id` are the agent's to report — letting an admin screen
         * write them would put the forum's idea of a server permanently out of
         * step with the host, with no way to tell which was lying.
         */
        foreach (['name', 'join_address', 'join_password', 'join_code'] as $field) {
            if (array_key_exists($field, $body)) {
                $value = trim((string) $body[$field]);
                $server->$field = $value === '' ? null : $value;
            }
        }

        if (array_key_exists('is_public', $body)) {
            $server->is_public = (bool) $body['is_public'];
        }

        if (array_key_exists('auto_remediate', $body)) {
            $server->auto_remediate = (bool) $body['auto_remediate'];
        }

        if (array_key_exists('backup_every_hours', $body)) {
            // Clamped rather than trusted: a typo of 0.5 or 100000 should not
            // become a backup every few seconds or one every eleven years.
            $hours = (int) $body['backup_every_hours'];
            $server->backup_every_hours = max(0, min(24 * 30, $hours));
        }

        if (array_key_exists('join_group_id', $body)) {
            $group = $body['join_group_id'];
            $server->join_group_id = ($group === null || $group === '') ? null : (int) $group;
        }

        /**
         * 🚨 Clearing needs_attention is a DELIBERATE act with a button behind
         * it, and the only way it ever gets cleared. The ladder never re-arms
         * itself: it gave up because restarting did not help, and a server
         * that happens to look fine one poll later has not proved the fault is
         * gone. A person says "I have looked".
         */
        if (! empty($body['clear_attention'])) {
            $server->needs_attention = false;
            $server->unready_polls = 0;
            $server->unready_since = null;
            $server->last_remediation_at = null;
        }

        $server->updated_at = Carbon::now();
        $server->save();

        return new JsonResponse(['ok' => true]);
    }

    /**
     * Fetch the game's real logo now, on request.
     *
     * The same service the scheduler uses, so a manual fetch and an automatic
     * one cannot drift apart — one of them being subtly different is how a
     * "try again" button becomes the only one that works.
     */
    protected function fetchIcon(ServerRequestInterface $request): ResponseInterface
    {
        $server = Server::query()->find((int) ($request->getQueryParams()['id'] ?? 0));

        if ($server === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        // A manual fetch clears the attempt count: the operator is explicitly
        // asking, and refusing because an automatic attempt failed yesterday
        // would be a button that does nothing.
        $server->icon_attempts = 0;
        $server->save();

        /**
         * An operator-supplied URL covers every game Garrison has no catalogue
         * entry for — Minecraft included, which has no Steam page and is the
         * most common dedicated server there is. Downloaded and kept, exactly
         * like the catalogue path: never a hotlink.
         */
        $given = trim((string) (($request->getParsedBody() ?? [])['url'] ?? ''));

        if ($given !== '') {
            $reason = null;
            $url = $this->artwork->fetchFrom($server, $given, $reason);

            if ($url === null) {
                throw new ValidationException([
                    'icon' => $this->translator->trans('ernestdefoe-garrison.api.errors.' . ($reason ?: 'fetch_failed')),
                ]);
            }

            return new JsonResponse(['iconUrl' => $url]);
        }

        $url = $this->artwork->fetch($server);

        if ($url === null) {
            throw new ValidationException([
                'icon' => $this->translator->trans('ernestdefoe-garrison.api.errors.fetch_failed'),
            ]);
        }

        return new JsonResponse(['iconUrl' => $url]);
    }

    /**
     * Upload an icon for a server.
     *
     * 🚨 An UPLOAD, never a URL field. Every image setting that only accepts a
     * URL ends up pointing at somebody else's server, and it rots — the image
     * vanishes months later and the forum owner has no idea why. This one
     * takes a file and keeps it.
     */
    protected function uploadIcon(ServerRequestInterface $request): ResponseInterface
    {
        $server = Server::query()->find((int) ($request->getQueryParams()['id'] ?? 0));

        if ($server === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        /** @var UploadedFileInterface|null $file */
        $file = ($request->getUploadedFiles()['icon'] ?? null);

        if ($file === null || $file->getError() !== UPLOAD_ERR_OK) {
            throw new ValidationException([
                'icon' => $this->translator->trans('ernestdefoe-garrison.api.errors.upload_failed'),
            ]);
        }

        /**
         * 🚨 The type is taken from the FILE, never from the client's
         * Content-Type or the filename's extension. Both are attacker-supplied
         * strings; a real image survives getimagesize and a disguised script
         * does not.
         */
        $temp = $file->getStream()->getMetadata('uri');
        $info = is_string($temp) ? @getimagesize($temp) : false;

        $allowed = [
            IMAGETYPE_PNG => 'png',
            IMAGETYPE_JPEG => 'jpg',
            IMAGETYPE_GIF => 'gif',
            IMAGETYPE_WEBP => 'webp',
        ];

        if ($info === false || ! isset($allowed[$info[2]])) {
            throw new ValidationException([
                'icon' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_an_image'),
            ]);
        }

        if ($file->getSize() > 512 * 1024) {
            throw new ValidationException([
                'icon' => $this->translator->trans('ernestdefoe-garrison.api.errors.too_large'),
            ]);
        }

        return new JsonResponse([
            'iconUrl' => $this->artwork->keep($server, $file->getStream()->getContents(), $allowed[$info[2]]),
        ]);
    }
}
