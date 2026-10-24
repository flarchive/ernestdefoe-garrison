<?php

use Flarum\Database\Migration;
use Illuminate\Database\Schema\Blueprint;

/**
 * One row per game server, as last reported by its agent.
 *
 * This table is a CACHE of what the agent said, not the truth. The agent is
 * the truth. Everything here exists so a page render never has to wait on a
 * round trip to a host that might be asleep — which is also why the whole
 * status page can be drawn from one query.
 */
return Migration::createTable('garrison_servers', function (Blueprint $table) {
    $table->increments('id');
    $table->unsignedInteger('agent_id');

    // The server's id ON THE AGENT. The forum never invents these: an agent
    // reports what it was configured with, and a name the forum has not been
    // told about is refused rather than created.
    $table->string('ref', 128);

    $table->string('name');
    $table->string('driver', 32);

    $table->string('state', 32)->default('unknown');
    $table->string('state_detail')->nullable();
    $table->unsignedInteger('pid')->nullable();
    $table->dateTime('running_since')->nullable();

    // Last stats sample, kept inline so the common render needs no join.
    $table->float('cpu_percent')->nullable();
    $table->unsignedBigInteger('memory_bytes')->nullable();
    $table->unsignedBigInteger('memory_limit')->nullable();
    $table->string('stats_source', 16)->nullable();

    $table->unsignedInteger('players_online')->nullable();
    $table->unsignedInteger('players_max')->nullable();

    $table->dateTime('last_status_at')->nullable();

    /**
     * Whether this server appears to people who are not staff, and what they
     * may see of it. Join details are gated separately because an address and
     * password are the one thing on this page that is worth stealing.
     */
    $table->boolean('is_public')->default(false);
    $table->unsignedInteger('join_group_id')->nullable();
    $table->string('join_address')->nullable();
    $table->string('join_password')->nullable();
    $table->string('join_code', 64)->nullable();

    $table->dateTime('created_at');
    $table->dateTime('updated_at')->nullable();

    // An agent cannot report two servers with the same ref, and the pair is
    // how every lookup arrives.
    $table->unique(['agent_id', 'ref']);
});
