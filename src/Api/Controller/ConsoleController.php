<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Http\RequestUtil;
use Illuminate\Database\ConnectionInterface;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * Reads a server's console.
 *
 * 🚨 Behind `garrison.console`, NOT `garrison.view`.
 *
 * A game console is not a status display. It carries chat, player names and
 * IPs, admin commands, mod errors with file paths, and whatever a plugin
 * decided to print. Showing it to everyone who can see that a server exists
 * would leak far more than the person granting "can view servers" intended.
 */
class ConsoleController implements RequestHandlerInterface
{
    public function __construct(
        protected ConnectionInterface $db
    ) {
    }

    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $actor = RequestUtil::getActor($request);

        if (! $actor->hasPermission('garrison.console') && ! $actor->hasPermission('garrison.manage')) {
            return new JsonResponse(['errors' => [['code' => 'not_permitted']]], 403);
        }

        $server = Server::query()->find((int) ($request->getQueryParams()['id'] ?? 0));

        if ($server === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        $params = $request->getQueryParams();

        /**
         * 🚨 `since` is an id, not a timestamp. Two lines can share a second —
         * a server that logs six things at once is completely ordinary — and
         * paging by time either repeats them or drops them. An id is
         * unambiguous.
         */
        $since = isset($params['since']) ? (int) $params['since'] : 0;
        $limit = min(500, max(1, (int) ($params['limit'] ?? 200)));

        $query = $this->db->table('garrison_console')
            ->where('agent_id', $server->agent_id)
            ->where('server_ref', $server->ref);

        if ($since > 0) {
            $rows = $query->where('id', '>', $since)->orderBy('id')->limit($limit)->get();
        } else {
            // The first read wants the MOST RECENT lines, then shown oldest
            // first — the bottom of a console is where the interesting part is.
            $rows = $query->orderByDesc('id')->limit($limit)->get()->reverse()->values();
        }

        return new JsonResponse([
            'lines' => $rows->map(fn ($r) => [
                'id' => (int) $r->id,
                'at' => $r->at,
                'text' => $r->text,
                'stderr' => (bool) $r->stderr,
            ])->all(),
            'canSend' => $actor->hasPermission('garrison.console') || $actor->hasPermission('garrison.manage'),
        ]);
    }
}
