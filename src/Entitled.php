<?php

namespace ErnestDefoe\Garrison;

use ErnestDefoe\Garrison\Model\GarrisonAgent;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Settings\SettingsRepositoryInterface;

/**
 * Which host and which server the free tier covers.
 *
 * 🚨 The cap needs a deterministic answer to *which one*, and the wrong answer
 * locks somebody out of the server they actually care about.
 *
 * So: the operator chooses, and until they do it is the oldest by id — stable,
 * and it does not move when a host goes offline or a name changes. The choice
 * matters most at the moment pro is removed, when somebody with three servers
 * has to be able to say which one stays live rather than have Garrison pick.
 *
 * 🚨 Nothing here DELETES or STOPS anything. A server outside the cap keeps
 * running, keeps being recorded, and keeps its backups and its history — it is
 * simply not shown on the forum and cannot be commanded. Installing pro later
 * restores a continuous record rather than one that begins the moment somebody
 * paid. See docs/entitlement.md §7.
 */
class Entitled
{
    public const AGENT_SETTING = 'ernestdefoe-garrison.entitled_agent_id';
    public const SERVER_SETTING = 'ernestdefoe-garrison.entitled_server_id';

    public function __construct(
        protected SettingsRepositoryInterface $settings
    ) {
    }

    /** Every server id this install may show and command, or null for all of them. */
    public function serverIds(): ?array
    {
        if (Edition::maxServers() === null) {
            return null;
        }

        return $this->pick(
            Server::query()->orderBy('id')->pluck('id')->all(),
            (int) $this->settings->get(self::SERVER_SETTING),
            Edition::maxServers()
        );
    }

    /** Every agent id this install may use, or null for all of them. */
    public function agentIds(): ?array
    {
        if (Edition::maxHosts() === null) {
            return null;
        }

        return $this->pick(
            GarrisonAgent::query()->orderBy('id')->pluck('id')->all(),
            (int) $this->settings->get(self::AGENT_SETTING),
            Edition::maxHosts()
        );
    }

    public function allowsServer(?int $id): bool
    {
        $ids = $this->serverIds();

        return $ids === null || ($id !== null && in_array($id, $ids, true));
    }

    public function allowsAgent(?int $id): bool
    {
        $ids = $this->agentIds();

        return $ids === null || ($id !== null && in_array($id, $ids, true));
    }

    /**
     * The chosen row first, then the oldest, up to the cap.
     *
     * 🚨 A chosen id that no longer exists falls back rather than yielding
     * nothing. An operator who deletes the server they had chosen must not end
     * up with a Garrison that shows none of the others — that reads as the
     * extension having broken, and it would happen at precisely the moment
     * somebody was already dealing with a deleted server.
     */
    private function pick(array $all, int $chosen, int $limit): array
    {
        $all = array_map('intval', $all);

        if ($chosen && in_array($chosen, $all, true)) {
            $all = [$chosen, ...array_values(array_filter($all, fn ($id) => $id !== $chosen))];
        }

        return array_slice($all, 0, $limit);
    }
}
