<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Model\Server;
use PHPUnit\Framework\TestCase;

/**
 * 🚨 A server whose only voice is a broadcast must not offer account linking.
 *
 * The verification code would be read out in global chat, so anybody could
 * claim "Admin", read Admin's code off the chat, and confirm it. Terraria's
 * built-in template was exactly that (`say {message}`).
 */
class BroadcastServersCannotLinkTest extends TestCase
{
    private function server(array $attributes): Server
    {
        $server = new Server();

        foreach ($attributes as $key => $value) {
            $server->{$key} = $value;
        }

        return $server;
    }

    public function testTheAgentsAnswerDecides(): void
    {
        $this->assertFalse($this->server(['players_known' => true, 'players_can_verify' => false, 'game' => 'minecraft'])->canVerifyPlayers());
        $this->assertTrue($this->server(['players_known' => true, 'players_can_verify' => true, 'game' => 'minecraft'])->canVerifyPlayers());
    }

    public function testAnOlderAgentFallsBackToTheGame(): void
    {
        $this->assertFalse($this->server(['players_known' => true, 'players_can_verify' => null, 'game' => 'terraria'])->canVerifyPlayers());
        $this->assertTrue($this->server(['players_known' => true, 'players_can_verify' => null, 'game' => 'minecraft'])->canVerifyPlayers());
    }

    public function testNoPlayersMeansNoLinking(): void
    {
        $this->assertFalse($this->server(['players_known' => false, 'players_can_verify' => true])->canVerifyPlayers());
    }

    /** The wiring: the flag the browser draws from is this method, not players_known. */
    public function testTheStatusPayloadUsesIt(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Api/Controller/ListServersController.php');

        $this->assertStringContainsString("\$row['canLink'] = \$server->canVerifyPlayers();", $source);
    }

    /** And the agent's answer is stored, or the method above only ever sees null. */
    public function testTheGatewayStoresTheAgentsAnswer(): void
    {
        $source = (string) file_get_contents(__DIR__ . '/../src/Agent/Gateway.php');

        $this->assertMatchesRegularExpression("/players_can_verify\\s*=.*\\n.*report\\['canVerify'\\]/", $source);
    }
}
