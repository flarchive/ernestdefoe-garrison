<?php

namespace ErnestDefoe\Garrison\Game;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Model\Server;
use ErnestDefoe\Garrison\Game\RemoteImage;
use Illuminate\Contracts\Filesystem\Factory;

/**
 * Gets a server its game's real logo, and keeps a copy.
 *
 * 🚨 This runs AUTOMATICALLY, because every other game panel shows the logo
 * and an operator should not have to go and find a button to get the normal
 * thing. A server that reports a known game gets its artwork on the next
 * scheduler tick without anybody asking.
 *
 * 🚨 Downloaded and STORED, never hotlinked. A hotlinked image is somebody
 * else's server deciding when your forum breaks — and when it does, months
 * later, nobody connects a missing picture to a URL they have not looked at
 * since.
 *
 * 🚨 And Garrison ships none of these files. Bundling a game's trade mark
 * inside a commercial extension puts the risk on the listing and on every
 * forum that installs it; a forum fetching artwork for its own page is the
 * ordinary thing, and it is the operator's site doing it.
 */
class Artwork
{
    /**
     * Give up after this many failed attempts at one game's artwork.
     *
     * 🚨 Bounded, because this is a scheduled job hitting a third party. A
     * game whose artwork 404s must not become a request every minute for ever
     * — that is how an extension gets a forum's IP rate-limited by somebody
     * else, for a decoration.
     */
    public const MAX_ATTEMPTS = 3;

    public function __construct(
        protected Factory $filesystem
    ) {
    }

    /**
     * Fetch artwork for every server that should have some and does not.
     *
     * @return int how many were given one
     */
    public function backfill(): int
    {
        $got = 0;

        Server::query()
            ->whereNull('icon_url')
            ->whereNotNull('game')
            ->where('icon_attempts', '<', self::MAX_ATTEMPTS)
            ->each(function (Server $server) use (&$got) {
                if ($this->fetch($server) !== null) {
                    $got++;
                }
            });

        return $got;
    }

    /**
     * Fetch one server's artwork. Returns the stored URL, or null.
     */
    public function fetch(Server $server): ?string
    {
        $candidates = Catalog::artworkCandidates($server->game);

        if ($candidates === []) {
            // Nothing to try is not a failure to retry — record it as spent so
            // the backfill stops considering this server every minute.
            $server->icon_attempts = self::MAX_ATTEMPTS;
            $server->save();

            return null;
        }

        foreach ($candidates as $url) {
            $bytes = $this->download($url);

            if ($bytes !== null) {
                $stored = $this->keep($server, $bytes[0], $bytes[1]);
                $server->icon_attempts = 0;
                $server->save();

                return $stored;
            }
        }

        $server->icon_attempts = (int) $server->icon_attempts + 1;
        $server->save();

        return null;
    }

    /**
     * Fetch from a URL the operator supplied.
     *
     * 🚨 Guarded by RemoteImage first. "Fetch this URL" hands the forum's own
     * network position to whoever typed the address; without the check,
     * http://127.0.0.1:6379/ and cloud metadata endpoints become things the
     * forum will connect to from inside the network.
     *
     * @return string|null the stored URL, or null with $reason set
     */
    public function fetchFrom(Server $server, string $url, ?string &$reason = null): ?string
    {
        $reason = RemoteImage::reject($url);

        if ($reason !== null) {
            return null;
        }

        $bytes = $this->download($url);

        if ($bytes === null) {
            $reason = 'fetch_failed';

            return null;
        }

        $stored = $this->keep($server, $bytes[0], $bytes[1]);
        $server->icon_attempts = 0;
        $server->save();

        return $stored;
    }

    /**
     * @return array{0: string, 1: string}|null [bytes, extension]
     */
    protected function download(string $url): ?array
    {
        $context = stream_context_create([
            'http' => [
                'timeout' => 10,
                // 🚨 No redirects. A redirect lands somewhere other than the
                // origin that was vetted, and following one turns a known
                // source into an arbitrary one.
                'follow_location' => 0,
                'header' => "User-Agent: Garrison/1.0 (Flarum extension)\r\n",
            ],
        ]);

        $handle = @fopen($url, 'rb', false, $context);

        if ($handle === false) {
            return null;
        }

        // 🚨 Bounded read. An unbounded fetch from a host that is not ours is
        // one oversized response away from exhausting PHP's memory limit and
        // taking the forum down with it.
        $bytes = @stream_get_contents($handle, 2 * 1024 * 1024);
        @fclose($handle);

        if ($bytes === false || strlen($bytes) < 100) {
            return null;
        }

        // 🚨 The type comes from the BYTES. Not the URL's extension, not any
        // header the far end chose to send — both are somebody else's strings.
        $info = @getimagesizefromstring($bytes);

        $allowed = [
            IMAGETYPE_PNG => 'png',
            IMAGETYPE_JPEG => 'jpg',
            IMAGETYPE_GIF => 'gif',
            IMAGETYPE_WEBP => 'webp',
        ];

        if ($info === false || ! isset($allowed[$info[2]])) {
            return null;
        }

        return [$bytes, $allowed[$info[2]]];
    }

    /**
     * Write the image, point the server at it, and remove the previous one.
     */
    public function keep(Server $server, string $bytes, string $extension): string
    {
        $disk = $this->filesystem->disk('flarum-assets');
        $path = 'garrison/' . $server->id . '-' . substr(bin2hex(random_bytes(8)), 0, 12) . '.' . $extension;

        // The old icon goes only AFTER the new one is safely written, so a
        // failure never leaves a server with no image at all.
        $previous = $server->icon_url;

        $disk->put($path, $bytes);
        $server->icon_url = $disk->url($path);
        $server->updated_at = Carbon::now();
        $server->save();

        if ($previous) {
            $old = 'garrison/' . basename(parse_url($previous, PHP_URL_PATH) ?: '');

            if (str_starts_with($old, 'garrison/') && $disk->exists($old)) {
                $disk->delete($old);
            }
        }

        return $server->icon_url;
    }
}
