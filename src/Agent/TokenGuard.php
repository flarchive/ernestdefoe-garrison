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

        if (! self::matches($secret, (string) $agent->token_hash)) {
            return null;
        }

        // A token minted before the sha256 change is upgraded on its first
        // good poll, so bcrypt runs at most once per agent from then on.
        if (! str_starts_with((string) $agent->token_hash, self::SHA256)) {
            $agent->token_hash = self::hash($secret);
            $agent->save();
        }

        return $agent;
    }

    /**
     * 🚨 sha256 + hash_equals, not password_verify.
     *
     * The secret is 192 random bits, not a password: nothing about it can be
     * guessed or looked up, so a slow hash buys a leaked database nothing a
     * fast one does not. What a slow hash DID buy was a bcrypt verify for
     * every unauthenticated poll that named a real agent id — CPU anybody on
     * the internet could spend. hash_equals keeps the comparison constant
     * time. Hashes from before the change are still bcrypt and still verify.
     */
    public static function matches(string $secret, string $stored): bool
    {
        if ($stored === '') {
            return false;
        }

        if (str_starts_with($stored, self::SHA256)) {
            return hash_equals($stored, self::hash($secret));
        }

        return password_verify($secret, $stored);
    }

    public static function hash(string $secret): string
    {
        return self::SHA256.hash('sha256', $secret);
    }

    private const SHA256 = 'sha256:';

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

        return [$agentId.'.'.$secret, self::hash($secret)];
    }
}
