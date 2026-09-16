<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * One stretch of time somebody spent in a game.
 *
 * 🚨 Built by DIFFING the set of players the agent reports each poll, not from
 * join and leave events. An events design never recovers from a dropped poll,
 * an agent restart or a log rotation — the forum would show somebody in a game
 * they left last Tuesday, and quietly count it as playtime. A set is
 * authoritative and self-correcting: whoever is missing from the latest report
 * is no longer playing, whatever the forum previously believed.
 */
return [
    'up' => function (Builder $schema) {
        if (! $schema->hasTable('garrison_sessions')) {
            $schema->create('garrison_sessions', function (Blueprint $table) {
                $table->increments('id');
                $table->unsignedInteger('server_id');

                // The in-game name, as the log reported it. NOT a forum user:
                // most players never link an account, and their playtime is
                // still worth showing on the server's own page.
                $table->string('player', 64);

                $table->dateTime('started_at');
                $table->dateTime('ended_at')->nullable();

                /*
                 * 🚨 Stored, not computed from the two timestamps on read.
                 *
                 * Totalling playtime across thousands of sessions is the one
                 * query this table exists to answer, and TIMESTAMPDIFF over
                 * every row cannot use an index. A column that is written once
                 * when a session closes makes it a SUM.
                 */
                $table->unsignedInteger('seconds')->default(0);

                $table->index(['server_id', 'player']);
                // Finding the open session for somebody is the hot path: it
                // happens once per player per poll, for ever.
                $table->index(['server_id', 'ended_at']);
            });
        }

        // Who is online right now, cached on the server row so the status
        // payload does not need a second query per server.
        if (! $schema->hasColumn('garrison_servers', 'players_online_names')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->text('players_online_names')->nullable();
            });
        }

        // 🚨 Distinguishes "nobody is playing" from "this server does not
        // report players". They look identical in an empty list and mean
        // opposite things on a panel.
        if (! $schema->hasColumn('garrison_servers', 'players_known')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->boolean('players_known')->default(false);
            });
        }
    },
    'down' => function (Builder $schema) {
        $schema->dropIfExists('garrison_sessions');

        foreach (['players_online_names', 'players_known'] as $name) {
            if ($schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', function (Blueprint $t) use ($name) {
                    $t->dropColumn($name);
                });
            }
        }
    },
];
