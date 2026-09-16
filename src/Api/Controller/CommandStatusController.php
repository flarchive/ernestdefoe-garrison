<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use ErnestDefoe\Garrison\Model\Command;
use Flarum\Http\RequestUtil;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * What happened to a command somebody queued.
 *
 * 🚨 THIS EXISTS BECAUSE "ACCEPTED" IS NOT AN OUTCOME.
 *
 * Queueing returns 202 on purpose — the agent has up to a poll window to pick
 * the command up, and pretending otherwise would have the UI claim a server
 * restarted before anything touched it. But a product that only ever says
 * "queued" has quietly moved the hardest part onto the operator: they click
 * Restore, see "queued", and then have no way to learn whether their world
 * came back or the archive was corrupt. The most consequential action in the
 * whole product would be the one with the least feedback.
 *
 * So the queue response's id is readable, and the UI polls it until the agent
 * answers. Slower than pretending, and it is the difference between a panel
 * that reports and a panel that guesses.
 */
class CommandStatusController implements RequestHandlerInterface
{
    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $actor = RequestUtil::getActor($request);
        $actor->assertRegistered();

        $id = (int) ($request->getQueryParams()['id'] ?? 0);

        /** @var Command|null $command */
        $command = Command::query()->find($id);

        if ($command === null) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        /*
         * 🚨 You may read a command if you queued it, or if you run the place.
         *
         * Not "anyone with garrison.view". A command's result can carry more
         * than its verb suggests — a failed console.send echoes the line that
         * failed, which is an operator typing an admin command — and the
         * person who queued it is the one who needs to know how it went.
         * Everybody else is reading somebody's actions, which is what the
         * audit log is for and is gated accordingly.
         */
        if ($command->actor_id !== $actor->id && ! $actor->hasPermission('garrison.manage')) {
            return new JsonResponse(['errors' => [['code' => 'not_found']]], 404);
        }

        return new JsonResponse([
            'data' => [
                'id' => $command->id,
                'verb' => $command->verb,
                'status' => $command->status,
                'errorCode' => $command->error_code,

                /*
                 * The agent's own message, not a translated one. It says
                 * things like "no such backup" or "server is running" — the
                 * host's answer in the host's words, which is what somebody
                 * debugging actually needs. Rendered as detail beneath a
                 * translated headline, never as the whole message.
                 */
                'errorMessage' => $command->error_message,
                'result' => $command->result === null ? null : json_decode($command->result, true),
                'completedAt' => $command->completed_at?->toIso8601String(),
            ],
        ]);
    }
}
