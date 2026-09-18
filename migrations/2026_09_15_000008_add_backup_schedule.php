<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Re-runnable, one column per statement — see 000005 for why.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            // 0 means no schedule. Not nullable, so "off" is a value rather
            // than an absence somebody has to remember to handle.
            'backup_every_hours' => fn (Blueprint $t) => $t->unsignedInteger('backup_every_hours')->default(0),
            'backup_queued_at' => fn (Blueprint $t) => $t->dateTime('backup_queued_at')->nullable(),
        ];

        foreach ($columns as $name => $define) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', $define);
            }
        }
    },
    'down' => function (Builder $schema) {
        foreach (['backup_every_hours', 'backup_queued_at'] as $name) {
            if ($schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', function (Blueprint $t) use ($name) {
                    $t->dropColumn($name);
                });
            }
        }
    },
];
