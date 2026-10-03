<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Re-runnable, one column per statement — see 000005 for why.
 *
 * The backup list the agent ships with every status report, cached here so the
 * forum can answer "is there anything to restore?" the instant somebody asks,
 * including while the host is offline. That question is only ever asked just
 * after something went badly wrong, which is the worst possible moment to show
 * a spinner and a round trip through a long poll.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            // 🚨 TEXT, not JSON. MariaDB aliases JSON to LONGTEXT anyway, and
            // a real JSON column on MySQL 5.7 rejects the NULL-as-absence this
            // code relies on in ways that differ by version. Every other
            // agent-reported blob in this schema (health_checks) is text for
            // the same reason; making this one different would be a trap for
            // whoever reads them side by side.
            'backups' => fn (Blueprint $t) => $t->text('backups')->nullable(),
        ];

        foreach ($columns as $name => $define) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', $define);
            }
        }
    },
    'down' => function (Builder $schema) {
        if ($schema->hasColumn('garrison_servers', 'backups')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->dropColumn('backups');
            });
        }
    },
];
