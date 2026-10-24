<?php

namespace ErnestDefoe\Garrison\Game;

/**
 * Decides whether a URL an operator typed is safe for the forum to fetch.
 *
 * 🚨 THIS IS AN SSRF GUARD, and it is needed even though only an admin can
 * reach it.
 *
 * "Fetch this URL" hands the forum's own network position to whoever typed the
 * address. Without checks, `http://127.0.0.1:6379/`, `http://10.0.0.5/` or a
 * cloud metadata endpoint all become things a forum will dutifully connect to
 * from inside the network — and the response comes back as an error message
 * that tells the sender what it found. Being admin-only narrows who can do it;
 * it does not make port-scanning the internal network from the forum an
 * acceptable capability, and it does not help at all once an admin account is
 * compromised.
 */
class RemoteImage
{
    /**
     * @return string|null the reason it was refused, or null if it is allowed
     */
    public static function reject(string $url): ?string
    {
        $parts = parse_url($url);

        if ($parts === false || empty($parts['host']) || empty($parts['scheme'])) {
            return 'not_a_url';
        }

        // 🚨 http and https only. file://, gopher://, php:// and friends are
        // all things a fetch helper will happily open otherwise.
        if (! in_array(strtolower($parts['scheme']), ['http', 'https'], true)) {
            return 'bad_scheme';
        }

        /**
         * 🚨 Strip the brackets from an IPv6 literal. parse_url returns
         * `[::1]` WITH them, and filter_var then fails to recognise it as an
         * IP at all — so it fell through to a DNS lookup and was refused as
         * "unresolvable". Blocked, but by accident: a near miss rather than a
         * control. An address this code cannot classify must never be the
         * thing standing between a forum and its own loopback interface.
         */
        $host = $parts['host'];

        if (str_starts_with($host, '[') && str_ends_with($host, ']')) {
            $host = substr($host, 1, -1);
        }

        // 🚨 Resolve and check EVERY address the name maps to. A hostname that
        // resolves to 127.0.0.1 is the standard way past a check that only
        // looked at the string — and a name with both a public and a private
        // record must be refused on the private one.
        $addresses = self::resolve($host);

        if ($addresses === []) {
            return 'unresolvable';
        }

        foreach ($addresses as $ip) {
            if (! self::isPublic($ip)) {
                return 'private_address';
            }
        }

        return null;
    }

    /** @return array<int, string> */
    protected static function resolve(string $host): array
    {
        // A literal address is its own answer.
        if (filter_var($host, FILTER_VALIDATE_IP)) {
            return [$host];
        }

        $out = [];

        foreach (['A' => DNS_A, 'AAAA' => DNS_AAAA] as $key => $type) {
            $records = @dns_get_record($host, $type) ?: [];

            foreach ($records as $record) {
                $ip = $record['ip'] ?? $record['ipv6'] ?? null;

                if ($ip !== null) {
                    $out[] = $ip;
                }
            }
        }

        return $out;
    }

    protected static function isPublic(string $ip): bool
    {
        return filter_var(
            $ip,
            FILTER_VALIDATE_IP,
            FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE
        ) !== false;
    }
}
