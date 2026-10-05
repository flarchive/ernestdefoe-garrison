<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * A forum account, and the in-game player it belongs to.
 *
 * 🚨 Proved in the GAME, not asserted on the forum. A form that just asks "what
 * is your in-game name?" links whatever somebody types, which means anybody can
 * claim the community's best-known player and inherit their playtime, their
 * rank and whatever an operator built on top of that. Garrison whispers a code
 * to that player inside the game; only the person holding that account reads
 * it, and they type it back here.
 */
return [
    'up' => function (Builder $schema) {
        if ($schema->hasTable('garrison_identities')) {
            return;
        }

        $schema->create('garrison_identities', function (Blueprint $table) {
            $table->increments('id');
            $table->unsignedInteger('user_id');

            /*
             * 🚨 Scoped to a SERVER, not to the forum.
             *
             * "alice" on the Minecraft server and "alice" on the Valheim server
             * are not necessarily the same person, and a forum-wide claim would
             * let somebody who verified on the small server they run inherit a
             * stranger's identity on the big one.
             */
            $table->unsignedInteger('server_id');
            $table->string('player', 64);

            // Null until the code has been typed back. An unverified row is a
            // pending claim and confers nothing.
            $table->dateTime('verified_at')->nullable();

            /*
             * 🚨 The code is stored HASHED, like the agent tokens are.
             *
             * It is short-lived and low-value, which is exactly the reasoning
             * that leads to storing it in plaintext — and then a read-only
             * database leak, or an admin browsing tables, hands somebody the
             * ability to complete a claim in flight. Hashing costs nothing
             * here and removes the question.
             */
            $table->string('code_hash', 255)->nullable();
            $table->dateTime('code_expires_at')->nullable();
            $table->unsignedSmallInteger('attempts')->default(0);

            $table->dateTime('created_at')->nullable();

            // One claim per user per server, and one player per server: both
            // directions have to be unique or the link means nothing.
            $table->unique(['user_id', 'server_id']);
            $table->unique(['server_id', 'player']);
        });
    },
    'down' => function (Builder $schema) {
        $schema->dropIfExists('garrison_identities');
    },
];
