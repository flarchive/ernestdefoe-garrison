<?php

namespace ErnestDefoe\Garrison\Health;

use Symfony\Contracts\Translation\TranslatorInterface;

/**
 * What a health report says, in the forum's language.
 *
 * The agent sends every finding twice: as English (`summary`, a result's
 * `detail`) and, from protocol 2, as a stable ID with params (`summaryId` +
 * `summaryParams`, a result's `id` + `params`). This turns the ID into a
 * sentence from the locale under `ernestdefoe-garrison.health.*`.
 *
 * 🚨 THE ENGLISH IS THE FALLBACK, AND BOTH DIRECTIONS NEED IT.
 *
 *   - An agent older than protocol 2 sends no ID at all.
 *   - An agent NEWER than this forum sends IDs this locale has never heard of.
 *
 * Either way the reader gets the agent's English rather than a raw key or a
 * blank line, so upgrading either half first never makes a finding disappear.
 *
 * The result is TEXT. Params are the agent's facts (a port, a log pattern) and
 * reach the page escaped, like any other string.
 */
final class HealthText
{
    public const PREFIX = 'ernestdefoe-garrison.health.';

    /*
     * The summary the agent sends for a failing probe is that probe's NAME,
     * which the operator wrote in their own config, in their own language.
     * There is nothing to translate, so it is shown exactly as written.
     */
    public const SUMMARY_PROBE_FAILED = 'probe_failed';

    public function __construct(private TranslatorInterface $translator)
    {
    }

    /**
     * @param array<string, mixed>|null $params
     */
    public function summary(?string $id, ?array $params, ?string $english): ?string
    {
        if ($id === self::SUMMARY_PROBE_FAILED) {
            $probe = $params['probe'] ?? null;

            return is_scalar($probe) && (string) $probe !== '' ? (string) $probe : $english;
        }

        return $this->lookup('summary', $id, $params ?? []) ?? $english;
    }

    /**
     * A result's detail line: translated from its id, else the agent's English.
     *
     * @param array<string, mixed> $check one entry of the agent's `results`
     */
    public function detail(array $check): string
    {
        $english = (string) ($check['detail'] ?? '');
        $id = isset($check['id']) && is_string($check['id']) ? $check['id'] : null;
        $params = isset($check['params']) && is_array($check['params']) ? $check['params'] : [];

        if ($id !== null && isset($params['within']) && is_scalar($params['within'])) {
            $params['within'] = $this->duration((string) $params['within']) ?? (string) $params['within'];
        }

        return $this->lookup('detail', $id, $params) ?? $english;
    }

    /**
     * 🚨 Only an id that LOOKS like one is ever spliced into a key. The id
     * comes off the wire from a host the forum does not control; a dot or a
     * slash in it must not walk the key into some other part of the locale.
     *
     * @param array<string, mixed> $params
     */
    private function lookup(string $kind, ?string $id, array $params): ?string
    {
        if ($id === null || ! preg_match('/^[a-z0-9_]{1,64}$/', $id)) {
            return null;
        }

        $key = self::PREFIX.$kind.'.'.$id;
        $out = $this->translator->trans($key, $this->scalars($params));

        // Flarum's translator hands the key back on a miss: an ID this forum
        // does not know yet. That is the fallback case, not something to show.
        return $out === $key ? null : $out;
    }

    /**
     * Scalars only, and every one as a STRING.
     *
     * A string because ICU formats a bare number with the locale's grouping:
     * port 2457 became "2,457", which is not a port anybody can type.
     *
     * @param array<string, mixed> $params
     * @return array<string, string>
     */
    private function scalars(array $params): array
    {
        $out = [];

        foreach ($params as $k => $v) {
            if (is_string($k) && (is_scalar($v) || $v === null)) {
                $out[$k] = (string) $v;
            }
        }

        return $out;
    }

    /**
     * A Go duration ("10m0s", "1h30m0s", "45s") said the way the rest of
     * Garrison says a span of time, or null if it is not one.
     */
    public function duration(string $go): ?string
    {
        if (! preg_match('/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/', $go, $m) || $go === '') {
            return null;
        }

        $hours = (int) ($m[1] ?? 0);
        $minutes = (int) ($m[2] ?? 0);
        $seconds = (float) ($m[3] ?? 0);

        // Below a minute the seconds are the whole answer; above, they are noise.
        if ($hours === 0 && $minutes === 0) {
            return $this->translator->trans(self::PREFIX.'seconds', ['count' => (int) round($seconds)]);
        }

        $forum = 'ernestdefoe-garrison.forum.duration.';

        if ($hours === 0) {
            return $this->translator->trans($forum.'minutes', ['count' => $minutes]);
        }

        if ($minutes === 0) {
            return $this->translator->trans($forum.'hours', ['count' => $hours]);
        }

        return $this->translator->trans($forum.'both', ['hours' => $hours, 'minutes' => $minutes]);
    }
}
