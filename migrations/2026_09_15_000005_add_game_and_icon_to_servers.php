<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * What game a server IS, and what to show for it.
 *
 * 🚨 Two columns, not one, and the split matters. `game` is a stable key the
 * agent reports from its manifest ("valheim", "minecraft"); `icon_url` is what
 * an operator chose to show. Collapsing them would mean the only way to
 * identify a game is by the picture somebody pasted, and every feature that
 * needs to KNOW the game — a health probe, a console dialect, a config schema —
 * would have nothing to key off.
 *
 * 🚨 WRITTEN TO BE RE-RUNNABLE, and that is not defensive padding.
 *
 * Laravel's MySQL grammar issues ONE `ALTER TABLE ... ADD` per column, so a
 * multi-column migration is several statements with no transaction around them
 * — MySQL and MariaDB cannot roll DDL back. Anything that interrupts the run
 * between them leaves some columns added and the migration NOT recorded, so
 * every retry dies on "Duplicate column name" and the extension can never
 * finish installing. It happened here, on the first run of this very file.
 *
 * Guarding each column turns that from an unrecoverable install into a retry
 * that simply works. The same failure has shipped to customers before, in two
 * other extensions, and left them half-installed and unusable.
 */
return [
    'up' => function (Builder $schema) {
        $columns = [
            'game' => fn (Blueprint $table) => $table->string('game', 64)->nullable(),
            'icon_url' => fn (Blueprint $table) => $table->string('icon_url')->nullable(),
        ];

        foreach ($columns as $name => $define) {
            if ($schema->hasColumn('garrison_servers', $name)) {
                continue;
            }

            // One table() call per column, so a failure on the second cannot
            // undo the first and each is independently retryable.
            $schema->table('garrison_servers', $define);
        }
    },

    'down' => function (Builder $schema) {
        foreach (['game', 'icon_url'] as $name) {
            if (! $schema->hasColumn('garrison_servers', $name)) {
                continue;
            }

            $schema->table('garrison_servers', function (Blueprint $table) use ($name) {
                $table->dropColumn($name);
            });
        }
    },
];
