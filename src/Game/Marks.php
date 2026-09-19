<?php

namespace ErnestDefoe\Garrison\Game;

/**
 * What to show beside a server's name.
 *
 * 🚨 Garrison does NOT ship game logos, and that is a deliberate legal choice
 * rather than an oversight.
 *
 * "Minecraft", "Valheim", "Rust" and the rest are registered trade marks, and
 * their logos are not licensed for redistribution inside a commercial product.
 * Bundling them would put every customer's forum — and this extension's
 * listing — on the wrong side of that, over decoration.
 *
 * So there are three tiers, resolved in this order:
 *
 *  1. `icon_url` — whatever the OPERATOR chose. Their forum, their call, and
 *     they may perfectly well use the real logo on their own site.
 *  2. A neutral MARK that ships with Garrison: a simple original glyph per
 *     game family, monochrome, inheriting the theme's colour. Recognisable as
 *     "the survival one" or "the block one" without imitating anything.
 *  3. A monogram from the server's own name, so a game nobody has drawn a mark
 *     for still gets something deliberate rather than a broken image.
 *
 * 🚨 Tier 1 must be an UPLOAD, never a URL field an operator pastes a hotlink
 * into. Every image field that only accepts a URL ends up pointing at somebody
 * else's server, and it rots — the image disappears months later and the forum
 * owner has no idea why.
 */
class Marks
{
    /**
     * Games Garrison ships a mark for. The key is what an agent reports.
     *
     * The value is the mark's file stem under resources/icons, kept separate
     * from the key so several games can share one family mark — a survival
     * game is a survival game, and drawing forty near-identical glyphs would
     * be worse than honest reuse.
     */
    public const MARKS = [
        'valheim' => 'longship',
        'minecraft' => 'block',
        'minecraft-bedrock' => 'block',
        'palworld' => 'sphere',
        'rust' => 'gear',
        'ark' => 'claw',
        'cs2' => 'crosshair',
        'csgo' => 'crosshair',
        'tf2' => 'crosshair',
        'satisfactory' => 'factory',
        'factorio' => 'factory',
        'terraria' => 'pickaxe',
        '7dtd' => 'pickaxe',
        'projectzomboid' => 'pickaxe',
        'enshrouded' => 'longship',
        'vrising' => 'claw',
    ];

    /** The mark used when the game is unknown but the server is not. */
    public const FALLBACK = 'server';

    /**
     * The mark stem for a game key, or null when there is none.
     */
    public static function forGame(?string $game): ?string
    {
        if ($game === null || $game === '') {
            return null;
        }

        return self::MARKS[strtolower($game)] ?? null;
    }

    /**
     * A one or two letter monogram from a server name.
     *
     * 🚨 Takes the initials of the first two WORDS, not the first two letters.
     * "Shattered Pact" is SP, which somebody recognises; "Sh" is noise. Falls
     * back to the first character for a one-word name.
     */
    public static function monogram(string $name): string
    {
        $words = preg_split('/[\s\-_]+/u', trim($name), -1, PREG_SPLIT_NO_EMPTY) ?: [];

        if ($words === []) {
            return '?';
        }

        if (count($words) === 1) {
            return mb_strtoupper(mb_substr($words[0], 0, 1));
        }

        return mb_strtoupper(mb_substr($words[0], 0, 1) . mb_substr($words[1], 0, 1));
    }
}
