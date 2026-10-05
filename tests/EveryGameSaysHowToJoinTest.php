<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Game\Catalog;
use PHPUnit\Framework\TestCase;

/**
 * 🚨 Every game Garrison recognises must be able to answer "how do I join?".
 *
 * A missing translation does not fail quietly here — Flarum renders the raw key,
 * so a player looking at a server page would read
 * `ernestdefoe-garrison.forum.connect.vrising` where the instructions should be.
 * That is visible to every member of the forum and reads as a broken product.
 *
 * Adding a game to the catalogue is therefore a promise to say how to join it,
 * and this is where that promise is kept.
 */
class EveryGameSaysHowToJoinTest extends TestCase
{
    /** @return array<string, mixed> */
    private function locale(): array
    {
        $yaml = (string) file_get_contents(__DIR__ . '/../resources/locale/en.yml');

        // Deliberately not a YAML parser: this only needs the keys under
        // `connect:`, and the test should not gain a dependency to read them.
        $out = [];
        $inConnect = false;

        foreach (explode("\n", $yaml) as $line) {
            if (preg_match('/^    connect:\s*$/', $line)) {
                $inConnect = true;
                continue;
            }

            if ($inConnect) {
                if (preg_match('/^      ([a-z0-9-]+):\s*(.+)$/', $line, $m)) {
                    $out[$m[1]] = trim($m[2]);
                    continue;
                }

                if (trim($line) !== '' && ! str_starts_with($line, '      ')) {
                    break;
                }
            }
        }

        return $out;
    }

    public function testEveryGameHasJoinInstructions(): void
    {
        $connect = $this->locale();

        $this->assertNotEmpty($connect, 'no connect strings were found at all');

        foreach (array_keys(Catalog::GAMES) as $game) {
            $this->assertArrayHasKey(
                $game,
                $connect,
                "{$game} is in the catalogue but has no join instructions, so its server page would print a raw translation key"
            );
        }
    }

    public function testEveryGameHasAUsualPort(): void
    {
        foreach (array_keys(Catalog::GAMES) as $game) {
            $this->assertNotNull(
                Catalog::port($game),
                "{$game} has no default port, so a server with no published address can say nothing useful"
            );
        }
    }

    public function testPortsAreRealPorts(): void
    {
        foreach (Catalog::PORTS as $game => $port) {
            $this->assertGreaterThan(1024, $port, "{$game}: a game server below 1024 would need root to bind");
            $this->assertLessThanOrEqual(65535, $port, "{$game}: not a port");
        }
    }

    public function testNoInstructionsForGamesThatDoNotExist(): void
    {
        // A connect string for a game the catalogue does not know is dead
        // weight that will never render, and usually a typo in the key.
        foreach (array_keys($this->locale()) as $game) {
            $this->assertArrayHasKey(
                $game,
                Catalog::GAMES,
                "there are join instructions for {$game}, which is not a game Garrison knows"
            );
        }
    }

    public function testTheInstructionsNameTheGame(): void
    {
        /*
         * 🚨 Each string should say WHICH game it is about. The panel is read
         * by somebody who may have several servers open, and "Multiplayer →
         * Add Server" with no game named is ambiguous between half the
         * catalogue.
         */
        foreach ($this->locale() as $game => $text) {
            $this->assertMatchesRegularExpression(
                '/^"?In /',
                $text,
                "{$game}: the instructions should start by naming the game"
            );
        }
    }
}
