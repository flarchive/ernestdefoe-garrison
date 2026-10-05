<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Re-runnable, one column per statement — see 000005 for why.
 *
 * The health summary as data (agent protocol 2): a stable ID the forum
 * translates, and the params that fill it in. `health_summary` keeps the
 * agent's English beside them and stays the fallback — for an agent older
 * than protocol 2, which sends no ID, and for an ID this forum does not know.
 *
 * The per-check IDs need no column: they arrive inside each result and are
 * stored with the rest of `health_checks`.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            'health_summary_id' => fn (Blueprint $t) => $t->string('health_summary_id', 64)->nullable(),
            'health_summary_params' => fn (Blueprint $t) => $t->text('health_summary_params')->nullable(),
        ];

        foreach ($columns as $name => $define) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', $define);
            }
        }
    },
    'down' => function (Builder $schema) {
        foreach (['health_summary_id', 'health_summary_params'] as $name) {
            if ($schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', function (Blueprint $t) use ($name) {
                    $t->dropColumn($name);
                });
            }
        }
    },
];
