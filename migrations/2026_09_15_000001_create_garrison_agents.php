<?php

use Flarum\Database\Migration;
use Illuminate\Database\Schema\Blueprint;

/**
 * One row per game host.
 *
 * 🚨 Built with the schema builder, never raw SQL. The builder applies the
 * database's table prefix; anything handed to selectRaw/whereRaw/statement does
 * not, and the resulting bug is invisible on any developer's forum and breaks
 * every customer who set a prefix. dev.ernestdefoe.online runs `dev_` for
 * exactly this reason.
 */
return Migration::createTable('garrison_agents', function (Blueprint $table) {
    $table->increments('id');
    $table->string('name');

    /**
     * 🚨 A HASH, never the token. The forum stores what it needs to verify a
     * presented token and nothing that could be replayed if the database
     * leaks. The plaintext is shown to the operator once, at pairing, and
     * never again — the same contract as an API key anywhere else.
     */
    $table->string('token_hash', 255);

    // Reported by the agent at every poll, so the forum can say what a host
    // can actually do rather than offering buttons that will fail.
    $table->string('version', 64)->nullable();
    $table->string('os', 32)->nullable();
    $table->string('arch', 32)->nullable();
    $table->text('drivers')->nullable();

    /**
     * An agent that has gone quiet is itself an incident — it is how you find
     * out a host died rather than a game. Indexed because the "which agents
     * are late" query runs on every dashboard render.
     */
    $table->dateTime('last_seen_at')->nullable()->index();

    $table->dateTime('created_at');
    $table->dateTime('updated_at')->nullable();
});
