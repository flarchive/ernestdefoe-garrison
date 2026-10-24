<?php

use Flarum\Database\Migration;
use Illuminate\Database\Schema\Blueprint;

/**
 * The command queue AND the audit log, deliberately one table.
 *
 * 🚨 Every privileged act is a row here before it is delivered, so there is no
 * path by which something happens on a game host without a record of who asked
 * for it. Keeping the audit separate would allow exactly that gap: a queue
 * write that succeeds and an audit write that fails.
 */
return Migration::createTable('garrison_commands', function (Blueprint $table) {
    $table->increments('id');
    $table->unsignedInteger('agent_id');
    $table->string('server_ref', 128)->nullable();

    $table->string('verb', 64);
    $table->text('params')->nullable();

    // queued | delivered | done | failed | expired
    $table->string('status', 16)->default('queued');

    $table->text('result')->nullable();
    $table->string('error_code', 64)->nullable();
    $table->text('error_message')->nullable();

    // Nullable because the scheduler and the health ladder also issue
    // commands, and "nobody" is a truthful answer that null records honestly.
    $table->unsignedInteger('actor_id')->nullable();
    $table->string('source', 32)->default('user'); // user | schedule | health

    $table->dateTime('created_at');
    $table->dateTime('delivered_at')->nullable();
    $table->dateTime('completed_at')->nullable();

    // The poll query is "queued commands for this agent, oldest first".
    $table->index(['agent_id', 'status', 'id']);
});
