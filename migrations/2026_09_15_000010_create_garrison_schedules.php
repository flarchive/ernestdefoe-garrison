<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * Scheduled work: restarts, backups, and lines sent to a console.
 *
 * 🚨 A TABLE, not more columns on `garrison_servers`.
 *
 * The existing `backup_every_hours` column is the shape this replaces: one
 * schedule, one kind of work, per server. Real communities want several —
 * a nightly restart, a backup before it, and a message to players an hour
 * beforehand — and every one of those added as a column pair is a migration,
 * an admin field, and a branch in the runner. A row per schedule costs one
 * table and makes the next kind of scheduled work a value rather than a
 * schema change.
 */
return [
    'up' => function (Builder $schema) {
        if ($schema->hasTable('garrison_schedules')) {
            return;
        }

        $schema->create('garrison_schedules', function (Blueprint $table) {
            $table->increments('id');
            $table->unsignedInteger('server_id');

            // restart | backup | console
            $table->string('kind', 20);

            /*
             * 🚨 Minutes past midnight, not a TIME column.
             *
             * A TIME is stored and compared in the DATABASE's timezone, which
             * on a shared host is whatever the provider set and is not
             * something an operator can see or change. An integer plus an
             * explicit timezone string is arithmetic this code does, in a zone
             * somebody chose on purpose — which is the difference between a
             * restart at 5am and a restart at 5am somewhere else.
             */
            $table->unsignedSmallInteger('at_minute');

            /*
             * 🚨 A 7-character mask, Monday first: "1111111" is every day.
             *
             * Deliberately not a cron expression. A cron field is more
             * powerful and it is a footgun in an admin panel: the operator
             * most likely to want a nightly restart is the one least likely to
             * write `0 5 * * *` correctly, and a schedule that is silently
             * wrong is worse than no schedule — it looks configured.
             */
            $table->string('days', 7)->default('1111111');

            $table->string('timezone', 64)->default('UTC');

            // For kind=console: the line to send. For kind=restart: unused.
            $table->text('payload')->nullable();

            /*
             * Advance warning, in minutes before the action, sent to the game
             * console. 0 means none. This is what every serious panel does and
             * it is the difference between a restart and an outage: players
             * get told, finish what they are doing, and log out.
             */
            $table->unsignedSmallInteger('warn_minutes')->default(0);
            $table->text('warn_payload')->nullable();

            $table->boolean('enabled')->default(true);

            /*
             * 🚨 Two marks, not one. `last_run_at` is when the action fired;
             * `last_warned_at` is when the warning did. Sharing a column would
             * make a warning look like a run and skip the action entirely —
             * a server that announces a restart every night and never restarts.
             */
            $table->dateTime('last_run_at')->nullable();
            $table->dateTime('last_warned_at')->nullable();

            $table->dateTime('created_at')->nullable();
            $table->dateTime('updated_at')->nullable();

            $table->index('server_id');
            $table->index('enabled');
        });
    },
    'down' => function (Builder $schema) {
        $schema->dropIfExists('garrison_schedules');
    },
];
