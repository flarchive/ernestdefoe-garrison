<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * Whether the agent says this server can whisper a verification code to one
 * player. Null for an agent too old to say, which falls back to the game.
 *
 * 🚨 Re-runnable — see 000005 for why.
 */
return [
    'up' => function (Builder $schema) {
        if (! $schema->hasColumn('garrison_servers', 'players_can_verify')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->boolean('players_can_verify')->nullable();
            });
        }
    },
    'down' => function (Builder $schema) {
        if ($schema->hasColumn('garrison_servers', 'players_can_verify')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->dropColumn('players_can_verify');
            });
        }
    },
];
