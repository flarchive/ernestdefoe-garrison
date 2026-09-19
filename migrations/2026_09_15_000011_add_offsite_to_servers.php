<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Re-runnable, one column per statement — see 000005 for why.
 *
 * What the agent reports about off-site copies. Deliberately only the OUTCOME:
 * whether it is configured, which bucket, and how the last copy went. The
 * credentials live in the agent's config file on the host and never reach the
 * forum — see driver.Server.Offsite for the reasoning, which is that the forum
 * is the part of this system most likely to be compromised.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            'offsite_configured' => fn (Blueprint $t) => $t->boolean('offsite_configured')->default(false),
            'offsite_bucket' => fn (Blueprint $t) => $t->string('offsite_bucket', 255)->nullable(),
            'offsite_last_at' => fn (Blueprint $t) => $t->dateTime('offsite_last_at')->nullable(),
            'offsite_last_ok' => fn (Blueprint $t) => $t->boolean('offsite_last_ok')->default(false),
            // 🚨 TEXT, because this holds the provider's own error — an S3
            // <Message> can be a paragraph, and truncating the one string that
            // says why somebody's off-site backups are failing would be a
            // strange place to save 200 bytes.
            'offsite_last_error' => fn (Blueprint $t) => $t->text('offsite_last_error')->nullable(),
        ];

        foreach ($columns as $name => $define) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', $define);
            }
        }
    },
    'down' => function (Builder $schema) {
        foreach (['offsite_configured', 'offsite_bucket', 'offsite_last_at', 'offsite_last_ok', 'offsite_last_error'] as $name) {
            if ($schema->hasColumn('garrison_servers', $name)) {
                $schema->table('garrison_servers', function (Blueprint $t) use ($name) {
                    $t->dropColumn($name);
                });
            }
        }
    },
];
