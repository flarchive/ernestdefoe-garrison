<?php

namespace ErnestDefoe\Garrison\Model;

use Carbon\Carbon;
use Flarum\Database\AbstractModel;

/**
 * One episode of a server being unhealthy, and what was done about it.
 *
 * 🚨 The `actions` log is the point of this record. An incident that says only
 * "the server was down" tells an operator nothing they did not already know.
 * One that says "unready at 05:11, restarted at 05:14, healthy at 05:16" is
 * the difference between trusting automatic remediation and switching it off.
 */
class Incident extends AbstractModel
{
    protected $table = 'garrison_incidents';

    protected $casts = [
        'started_at' => 'datetime',
        'resolved_at' => 'datetime',
    ];

    public function server()
    {
        return $this->belongsTo(Server::class, 'server_id');
    }

    /** @return array<int, array{at: string, what: string}> */
    public function actionList(): array
    {
        if (empty($this->actions)) {
            return [];
        }

        $decoded = json_decode($this->actions, true);

        return is_array($decoded) ? $decoded : [];
    }

    public function appendAction(string $what): void
    {
        $list = $this->actionList();
        $list[] = ['at' => Carbon::now()->toIso8601String(), 'what' => $what];

        // Bounded: a flapping server could otherwise write until the column
        // truncates mid-JSON, which loses the whole history rather than the
        // oldest part of it.
        if (count($list) > 50) {
            $list = array_slice($list, -50);
        }

        $this->actions = json_encode($list);
    }

    /**
     * Append only if the same line is not already the most recent one.
     *
     * For states the ladder re-observes on every tick — "auto-remediation is
     * off" would otherwise be written every thirty seconds forever and bury
     * everything that actually happened.
     */
    public function appendActionOnce(string $what): void
    {
        $list = $this->actionList();

        if ($list !== [] && end($list)['what'] === $what) {
            return;
        }

        $this->appendAction($what);
    }
}
