<?php

use Illuminate\Database\Schema\Blueprint;
use Illuminate\Database\Schema\Builder;

/**
 * 🚨 Bounds the automatic artwork fetch.
 *
 * Without a counter, a game whose logo 404s becomes an outbound request every
 * minute for ever — which is how an extension gets a forum's IP rate-limited
 * by a third party, over a decoration.
 */
return [
    'up' => function (Builder $schema) {
        if (! $schema->hasColumn('garrison_servers', 'icon_attempts')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->unsignedInteger('icon_attempts')->default(0);
            });
        }
    },
    'down' => function (Builder $schema) {
        if ($schema->hasColumn('garrison_servers', 'icon_attempts')) {
            $schema->table('garrison_servers', function (Blueprint $t) {
                $t->dropColumn('icon_attempts');
            });
        }
    },
];
