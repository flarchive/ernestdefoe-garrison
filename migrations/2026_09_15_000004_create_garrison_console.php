<?php

use Flarum\Database\Migration;
use Illuminate\Database\Schema\Blueprint;

/**
 * Console lines the agent has shipped up, pruned on a schedule.
 *
 * Retained at all because the first thing anybody does with a crashed server
 * is open the console and scroll up, and the agent's own buffer dies with the
 * agent.
 */
return Migration::createTable('garrison_console', function (Blueprint $table) {
    $table->increments('id');
    $table->unsignedInteger('agent_id');
    $table->string('server_ref', 128);
    $table->dateTime('at');
    $table->text('text');
    $table->boolean('stderr')->default(false);

    // Both real queries: "this server's recent lines" and "prune old ones".
    $table->index(['agent_id', 'server_ref', 'id']);
    $table->index('at');
});
