<?php

namespace ErnestDefoe\Garrison\Players;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Model\PlaySession;
use ErnestDefoe\Garrison\Model\Server;

/**
 * Turns "who is online now" into sessions.
 *
 * 🚨 THE AGENT'S SET IS AUTHORITATIVE, ALWAYS.
 *
 * Every poll carries the whole list of who is in the game. This opens a session
 * for anybody new and closes one for anybody missing — and it does the second
 * part unconditionally, which is what makes the whole design self-correcting.
 * A dropped poll, an agent restart, a log rotation, a crash that printed no
 * goodbyes: all of them are fixed by the next report, because the forum never
 * has to reason about what it might have missed. It only ever asks "who is
 * playing?" and believes the answer.
 */
class Tracker
{
    /** 24 hours. See close(). */
    public const MAX_SESSION_SECONDS = 86400;

    /**
     * @param array<int, string>|null $online null means the server does not
     *                                        report players at all, which is
     *                                        different from reporting nobody.
     */
    public function sync(Server $server, ?array $online): void
    {
        if ($online === null) {
            /*
             * 🚨 A server that stopped reporting players does NOT get its
             * sessions closed.
             *
             * The realistic cause is an operator removing the configuration,
             * or an agent downgraded to a version without it. Closing every
             * session would write a pile of endings that never happened and
             * lose whoever was mid-session; leaving them open costs nothing,
             * because the next real report reconciles them.
             */
            return;
        }

        $names = $this->clean($online);

        $open = PlaySession::query()
            ->where('server_id', $server->id)
            ->open()
            ->get()
            ->keyBy('player');

        foreach ($names as $name) {
            if ($open->has($name)) {
                continue;
            }

            $session = new PlaySession();
            $session->server_id = $server->id;
            $session->player = $name;
            $session->started_at = Carbon::now();
            $session->save();
        }

        foreach ($open as $player => $session) {
            if (in_array($player, $names, true)) {
                continue;
            }

            $this->close($session);
        }
    }

    /**
     * Close every open session for a server.
     *
     * 🚨 Called when a server stops or its agent goes silent. Left alone, a
     * crashed server's sessions run until somebody notices — and every hour of
     * downtime is counted as playtime for whoever happened to be on when it
     * fell over, which is worse than useless: a leaderboard that rewards being
     * online during an outage.
     */
    public function closeAll(Server $server): int
    {
        $sessions = PlaySession::query()
            ->where('server_id', $server->id)
            ->open()
            ->get();

        foreach ($sessions as $session) {
            $this->close($session);
        }

        return $sessions->count();
    }

    protected function close(PlaySession $session): void
    {
        $ended = Carbon::now();

        $session->ended_at = $ended;

        /*
         * 🚨 Clamped, and the clamp is not paranoia.
         *
         * A session's start comes from the forum's clock and its end from the
         * same clock, so normally this is fine — but a server whose agent was
         * offline for a week comes back and closes a session that has been open
         * since then. Counting 168 hours of playtime for somebody who played
         * twenty minutes ruins every total on the forum, permanently, and there
         * is nothing in the data afterwards to say which rows are wrong.
         *
         * A day is far longer than any real continuous session and far shorter
         * than the outages that cause this.
         */
        $seconds = max(0, $ended->getTimestamp() - $session->started_at->getTimestamp());

        $session->seconds = min($seconds, self::MAX_SESSION_SECONDS);
        $session->save();
    }

    /**
     * 🚨 Names are cleaned HERE, once, rather than trusted.
     *
     * They come from a game log, and in most games a player chooses their own
     * display name. The agent already applies sane bounds; this is the forum's
     * own check, because the forum is where these become rows, links and
     * profile pages — and the two halves failing differently is the point of
     * having both.
     *
     * @param array<int, mixed> $online
     * @return array<int, string>
     */
    protected function clean(array $online): array
    {
        $out = [];

        foreach ($online as $name) {
            if (! is_string($name)) {
                continue;
            }

            $name = trim($name);

            if ($name === '' || mb_strlen($name) > 64) {
                continue;
            }

            $out[$name] = true;
        }

        return array_keys($out);
    }
}
