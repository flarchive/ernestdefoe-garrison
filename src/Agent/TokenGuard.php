<?php

namespace ErnestDefoe\Garrison\Agent;

use ErnestDefoe\Garrison\Model\GarrisonAgent;
use Psr\Http\Message\ServerRequestInterface;

/**
 * Authenticates an agent from its bearer token.
 *
 * 🚨 The token is `<id>.<secret>` and only a HASH of the secret is stored.
 *
 * The id half is not decoration: without it, verifying a presented token means
 * hashing it against every agent row in turn, which is both slow and a timing
 * oracle for how many agents exist. With it there is exactly one candidate row
 * and one comparison.
 */
class TokenGuard
{
    /**
     * Resolve the agent a request is authenticated as, or null.
     */
    public function authenticate(ServerRequestInterface $request): ?GarrisonAgent
    {
        $header = $request->getHeaderLine('Authorization');

        if (! str_starts_with($header, 'Bearer ')) {
            return null;
        }

        return $this->fromToken(substr($header, 7));
    }

    public function fromToken(string $token): ?GarrisonAgent
    {
        $token = trim($token);

        if ($token === '' || ! str_contains($token, '.')) {
            return null;
        }

        [$id, $secret] = explode('.', $token, 2);

        if (! ctype_digit($id) || $secret === '') {
            return null;
        }

        /** @var GarrisonAgent|null $agent */
        $agent = GarrisonAgent::query()->find((int) $id);

        if ($agent === null) {
            return null;
        }

        // 🚨 password_verify, not hash_equals on a sha256. The stored value is
        // a password hash precisely so a leaked database is not a list of
        // usable agent tokens, and it brings the constant-time comparison with
        // it.
        if (! password_verify($secret, $agent->token_hash)) {
            return null;
        }

        return $agent;
    }

    /**
     * Mint a token for a newly paired agent.
     *
     * Returns the plaintext, which the caller shows ONCE. Nothing stores it.
     *
     * @return array{0: string, 1: string} [plaintext, hash]
     */
    public static function mint(int $agentId): array
    {
        $secret = bin2hex(random_bytes(24));

        return [$agentId . '.' . $secret, password_hash($secret, PASSWORD_DEFAULT)];
    }
}
