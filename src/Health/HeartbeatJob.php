<?php

namespace ErnestDefoe\Garrison\Health;

use Carbon\Carbon;
use Flarum\Queue\AbstractJob;
use Flarum\Settings\SettingsRepositoryInterface;

/**
 * Writes down the fact that a worker ran.
 *
 * 🚨 Deliberately the smallest job that could possibly prove anything: one
 * settings write, no I/O, no model loading. It rides the same queue every
 * Garrison alert rides, so if this lands, an alert would have landed.
 *
 * The dependency is resolved in handle() rather than the constructor, because a
 * queued job is SERIALISED between push and run. A constructor-injected
 * settings repository would be serialised with it — a live database connection
 * inside a Redis payload — which either fails to serialise or, worse,
 * deserialises into something stale.
 */
class HeartbeatJob extends AbstractJob
{
    public function handle(SettingsRepositoryInterface $settings): void
    {
        $settings->set(Heartbeat::RAN, Carbon::now()->toIso8601String());
    }
}
