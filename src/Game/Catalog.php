<?php

namespace ErnestDefoe\Garrison\Game;

/**
 * Known games, and where their real artwork lives.
 *
 * 🚨 Garrison ships NO logo files, and this class is why it does not need to.
 *
 * Bundling Minecraft's or Valheim's logo inside a commercial extension is
 * redistributing somebody else's trade mark — the risk lands on the listing
 * and on every forum that installs it. But a forum fetching the game's own
 * artwork for its own page is the ordinary thing every game panel does, and it
 * is the operator's site making that call.
 *
 * So: one click fetches the real logo from the game's own store page and
 * stores it LOCALLY. Not a hotlink — a hotlinked image is somebody else's
 * server deciding when your forum breaks, and it rots quietly months later.
 */
class Catalog
{
    /**
     * game key => [steam app id, human name].
     *
     * Steam is the source because a dedicated-server game almost always has a
     * store page, and the CDN path is stable and public.
     *
     * 🚨 A null app id does NOT mean "no artwork". Minecraft — the single most
     * common dedicated server there is — has never been on Steam, and leaving
     * the most popular game in the catalogue with no logo made the whole
     * feature look broken. Games without a Steam page get artwork through
     * EXPLICIT, which is the same fetch-and-store mechanism pointed somewhere
     * else.
     */
    public const GAMES = [
        'valheim' => [892970, 'Valheim'],
        'minecraft' => [null, 'Minecraft'],
        'minecraft-bedrock' => [null, 'Minecraft: Bedrock Edition'],
        'palworld' => [1623730, 'Palworld'],
        'rust' => [252490, 'Rust'],
        'ark' => [2399830, 'ARK: Survival Ascended'],
        'cs2' => [730, 'Counter-Strike 2'],
        'satisfactory' => [526870, 'Satisfactory'],
        'factorio' => [427520, 'Factorio'],
        '7dtd' => [251570, '7 Days to Die'],
        'projectzomboid' => [108600, 'Project Zomboid'],
        'terraria' => [105600, 'Terraria'],
        'enshrouded' => [1203620, 'Enshrouded'],
        'vrising' => [1604030, 'V Rising'],
        'dayz' => [221100, 'DayZ'],
        'gmod' => [4000, "Garry's Mod"],
    ];

    /**
     * Artwork for games that have no Steam page.
     *
     * 🚨 Still a FETCH, never a bundled file and never a hotlink — the bytes
     * are downloaded once and kept on the forum, exactly as the Steam path
     * does. The only difference is where they are read from.
     *
     * Kept deliberately short. A URL that 404s is worse than no entry at all:
     * it costs three attempts and then leaves the operator with the fallback
     * anyway, having looked like the feature failed.
     */
    public const EXPLICIT = [
        // 🚨 Deliberately EMPTY, and that is a finding rather than an omission.
        //
        // The obvious entry here was a Mojang logo URL for Minecraft, the most
        // common dedicated server there is. It was checked from the forum host
        // before shipping and came back UNREACHABLE — so it would have cost
        // three fetch attempts per server and then shown the fallback anyway,
        // while looking like the feature was broken.
        //
        // A URL I cannot verify is worse than no entry, so games without a
        // Steam page are served by the operator pasting a link once (fetched
        // and stored, never hotlinked) or uploading a file. Both cover every
        // game, and neither requires me to guess.
    ];

    public static function name(?string $game): ?string
    {
        if ($game === null) {
            return null;
        }

        return self::GAMES[strtolower($game)][1] ?? null;
    }

    public static function steamAppId(?string $game): ?int
    {
        if ($game === null) {
            return null;
        }

        return self::GAMES[strtolower($game)][0] ?? null;
    }

    /**
     * Candidate artwork URLs for a game, best first.
     *
     * 🚨 Several candidates because Steam does not guarantee every asset for
     * every app. `logo.png` is a transparent wordmark and much the nicest in a
     * list; `header.jpg` always exists but is a wide banner; `capsule` sits in
     * between. Trying in order means a game with no logo still gets something
     * rather than an error the operator has to interpret.
     *
     * @return array<int, string>
     */
    public static function artworkCandidates(?string $game): array
    {
        $key = strtolower((string) $game);
        $appId = self::steamAppId($game);

        $candidates = [];

        if ($appId !== null) {
            $base = 'https://cdn.cloudflare.steamstatic.com/steam/apps/' . $appId . '/';

            $candidates[] = $base . 'logo.png';
            $candidates[] = $base . 'capsule_231x87.jpg';
            $candidates[] = $base . 'header.jpg';
        }

        foreach (self::EXPLICIT[$key] ?? [] as $url) {
            $candidates[] = $url;
        }

        return $candidates;
    }
}
