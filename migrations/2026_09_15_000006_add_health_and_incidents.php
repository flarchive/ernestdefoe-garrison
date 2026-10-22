<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Re-runnable, one column per statement — see 000005. Laravel issues one
 * ALTER per column and MySQL cannot roll DDL back, so an interruption between
 * them leaves the migration unrecorded and every retry colliding.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            // ok | unready | down | unknown, as the agent reported it.
            'health_state' => fn (Blueprint $t) => $t->string('health_state', 16)->nullable(),
            'health_summary' => fn (Blueprint $t) => $t->string('health_summary')->nullable(),
            'health_checks' => fn (Blueprint $t) => $t->text('health_checks')->nullable(),

            /**
             * 🚨 How many consecutive polls have said unready.
             *
             * The ladder acts on a RUN, never on one reading. A single bad
             * poll is a network hiccup; three in a row is a server. Restarting
             * on the first would make Garrison the most common cause of the
             * outages it exists to prevent.
             */
            'unready_since' => fn (Blueprint $t) => $t->dateTime('unready_since')->nullable(),
            'unready_polls' => fn (Blueprint $t) => $t->unsignedInteger('unready_polls')->default(0),

            // When the ladder last acted, so flapping can be detected.
            'last_remediation_at' => fn (Blueprint $t) => $t->dateTime('last_remediation_at')->nullable(),

            /**
             * 🚨 Set when the ladder gives up. Nothing automatic touches a
             * server in this state again — it has proved that restarting does
             * not fix it, and a loop of restarts is worse than a server that
             * is honestly down and says so.
             */
            'needs_attention' => fn (Blueprint $t) => $t->boolean('needs_attention')->default(false),

            // Per-server switch, because an operator may want watching without
            // acting on a server they are mid-way through changing.
            'auto_remediate' => fn (Blueprint $t) => $t->boolean('auto_remediate')->default(true),
        ];

        foreach ($columns as $name => $define) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', $define);
            }
        }

        if (! $schema->hasTable('garrison_incidents')) {
            $schema->create('garrison_incidents', function (Blueprint $table) {
                $table->increments('id');
                $table->unsignedInteger('server_id');

                $table->dateTime('started_at');
                $table->dateTime('resolved_at')->nullable();

                /**
                 * 🚨 `cause`, not `trigger`. TRIGGER is a reserved word in
                 * MySQL and MariaDB: the schema builder quotes it so the table
                 * is created fine, and then every hand-written query against
                 * it dies with a syntax error that names the line AFTER the
                 * real problem. Caught by a diagnostic script before any
                 * customer had the column.
                 */
                $table->string('cause');
                $table->text('detail')->nullable();

                /**
                 * 🚨 The ladder's own log: what was tried, in order, and what
                 * happened. An incident that says only "server was down" tells
                 * an operator nothing they did not already know; one that says
                 * "restarted at 05:14, healthy at 05:16" is the difference
                 * between trusting this and turning it off.
                 */
                $table->text('actions')->nullable();

                // open | resolved | abandoned
                $table->string('status', 16)->default('open');
                $table->unsignedInteger('restarts')->default(0);

                $table->index(['server_id', 'status']);
                $table->index('started_at');
            });
        }
    },

    'down' => function (Builder $schema) {
        if ($schema->hasTable('garrison_incidents')) {
            $schema->drop('garrison_incidents');
        }
    },
];
