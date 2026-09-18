<?php

namespace ErnestDefoe\Garrison\Api\Controller;

use ErnestDefoe\Garrison\Agent\Gateway;
use ErnestDefoe\Garrison\Agent\TokenGuard;
use Laminas\Diactoros\Response\JsonResponse;
use Psr\Http\Message\ResponseInterface;
use Psr\Http\Message\ServerRequestInterface;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * The endpoint an agent long-polls.
 *
 * 🚨 Deliberately NOT a Flarum API resource and not behind Flarum's session
 * middleware. An agent is not a user: it has no session, no CSRF token and no
 * cookie, and routing it through the machinery built for browsers would mean
 * either weakening that machinery or teaching the agent to pretend. It
 * authenticates with a bearer token, on its own route, and that is all.
 */
class AgentPollController implements RequestHandlerInterface
{
    public function __construct(
        protected TokenGuard $guard,
        protected Gateway $gateway
    ) {
    }

    public function handle(ServerRequestInterface $request): ResponseInterface
    {
        $agent = $this->guard->authenticate($request);

        if ($agent === null) {
            // 🚨 No detail. "Unknown agent" and "wrong secret" must look
            // identical, or the response is an oracle for which agent ids
            // exist.
            return new JsonResponse(['error' => 'unauthorised'], 401);
        }

        $body = (array) ($request->getParsedBody() ?? []);

        // The poll carries the agent's own report, so a healthy agent needs no
        // second request to stay current: one round trip is liveness, status
        // and stats together.
        $this->gateway->touch($agent, (array) ($body['info'] ?? []));

        foreach ((array) ($body['servers'] ?? []) as $report) {
            if (is_array($report)) {
                $this->gateway->recordStatus($agent, $report);
            }
        }

        $this->gateway->recordConsole($agent, (array) ($body['console'] ?? []));

        /*
         * 🚨 `events`, which the forum ignored entirely until 1.2.2.
         *
         * The agent has always shipped an event stream alongside the console —
         * it is how anything that is not a log line reports itself. Nothing
         * here read it, so every event was posted, accepted with a 200 and
         * thrown away.
         *
         * What that hid: provisioning. An install runs in a goroutine long
         * after its command has been answered, and every word it has to say —
         * "downloading", "install failed: …", "installed, but this agent
         * cannot persist new servers" — goes through that stream. So a failed
         * install looked exactly like a successful one: the command said
         * `started: true`, and then silence for ever. Found by installing a
         * game and watching nothing happen.
         */
        $this->gateway->recordEvents($agent, (array) ($body['events'] ?? []));

        foreach ((array) ($body['results'] ?? []) as $result) {
            if (! is_array($result) || ! isset($result['id'])) {
                continue;
            }

            $this->gateway->recordResult(
                $agent,
                (int) $result['id'],
                (bool) ($result['ok'] ?? false),
                isset($result['data']) && is_array($result['data']) ? $result['data'] : null,
                $result['errorCode'] ?? null,
                $result['errorMessage'] ?? null
            );
        }

        // Only now hold the connection. Reporting first means a restart is
        // recorded even if the poll window is cut short by a proxy.
        $commands = $this->gateway->awaitCommands($agent);

        return new JsonResponse([
            'commands' => array_map(static fn ($c) => [
                'id' => $c->id,
                'verb' => $c->verb,
                'server' => $c->server_ref,

                /*
                 * 🚨 (object) is load-bearing. PHP encodes an empty array as
                 * `[]`, and the agent unmarshals params into a STRUCT — which
                 * fails on a JSON array, so every command with no parameters
                 * came back `bad_request`. Found by running it: `server.stop`
                 * with no explicit grace failed while `server.start`, which
                 * reads no params, succeeded. A typed client and an untyped
                 * encoder disagree exactly here, and only ever on the empty
                 * case, which is the one nobody writes a test for.
                 */
                'params' => (object) $c->paramsArray(),
            ], $commands),
            'pollSeconds' => Gateway::POLL_SECONDS,
        ]);
    }
}
