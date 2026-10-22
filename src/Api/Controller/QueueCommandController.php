<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Http\RequestUtil;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * Queue one command against one server.
 *
 * 🚨 The ONLY way anything is queued. Dispatcher::queue does the permission
 * check, so every route to a game host passes through one gate — the commonest
 * version of this bug is a second call site added later that forgets, and it
 * is invisible because the feature works.
 */
class QueueCommandController implements RequestHandlerInterface
{
    public function __construct(
        protected Dispatcher $dispatcher
    ) {
    }

    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $actor = RequestUtil::getActor($request);
        $actor->assertRegistered();

        $id = (int) ($request->getQueryParams()['id'] ?? 0);
        $body = (array) ($request->getParsedBody() ?? []);

        /** @var Server|null $server */
        $server = Server::query()->find($id);

        if ($server === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        $command = $this->dispatcher->queue(
            $actor,
            $server,
            (string) ($body['verb'] ?? ''),
            is_array($body['params'] ?? null) ? $body['params'] : []
        );

        // 202, not 200. The command is queued, not done — the agent has up to
        // a poll window to pick it up, and telling the UI "accepted" lets it
        // show that honestly instead of pretending the server already
        // restarted.
        return new JsonResponse([
            'data' => ['id' => $command->id, 'status' => $command->status],
        ], 202);
    }
}
