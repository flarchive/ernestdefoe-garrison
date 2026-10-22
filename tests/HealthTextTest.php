<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Health\HealthText;
use PHPUnit\Framework\TestCase;
use Symfony\Component\Translation\Loader\YamlFileLoader;
use Symfony\Component\Translation\Translator;

/**
 * Health findings, translated from the agent's IDs — and the fallback to its
 * English, in BOTH directions of version skew.
 *
 * 🚨 Every way this can be wrong is quiet. A missing key prints the key on a
 * server page; a dropped fallback prints nothing at all where an operator is
 * looking for why their server is unjoinable. Neither throws.
 *
 * Loaded through Symfony's translator with the ICU domain, the way Flarum
 * loads this file, so a malformed MessageFormat string fails here too.
 */
class HealthTextTest extends TestCase
{
    private HealthText $text;

    protected function setUp(): void
    {
        parent::setUp();

        $translator = new Translator('en');
        $translator->addLoader('yaml', new YamlFileLoader());
        $translator->addResource('yaml', __DIR__.'/../resources/locale/en.yml', 'en', 'messages+intl-icu');

        $this->text = new HealthText($translator);
    }

    public function test_a_known_detail_id_is_translated_from_its_params(): void
    {
        $out = $this->text->detail([
            'detail' => 'UDP 2457 has 9600 bytes queued and unread — the socket is open but nothing is reading it',
            'id' => 'udp_queue_stuck',
            'params' => ['port' => 2457, 'bytes' => 9600],
        ]);

        // 🚨 "2457", never "2,457": ICU groups a bare number, and a port with a
        // comma in it is not one anybody can type.
        $this->assertSame('UDP 2457 has 9600 bytes queued and unread — the socket is open but nothing is reading it', $out);
    }

    public function test_every_id_the_agent_can_send_has_a_sentence(): void
    {
        $ids = [
            'udp_queue_stuck' => ['port' => 1, 'bytes' => 2],
            'udp_queue' => ['port' => 1, 'bytes' => 2],
            'tcp_refused' => ['port' => 1],
            'tcp_accepting' => ['port' => 1],
            'no_port' => [],
            'log_absent' => ['pattern' => 'p'],
            'log_absent_within' => ['pattern' => 'p', 'within' => '10m0s'],
            'log_found' => ['pattern' => 'p'],
            'unknown_type' => ['type' => 't'],
        ];

        foreach ($ids as $id => $params) {
            $out = $this->text->detail(['detail' => 'ENGLISH FALLBACK', 'id' => $id, 'params' => $params]);
            $this->assertNotSame('ENGLISH FALLBACK', $out, "No locale sentence for detail id {$id}");
            $this->assertStringNotContainsString('{', $out, "Unfilled placeholder in {$id}: {$out}");
        }

        foreach (['not_running', 'no_probes', 'none_evaluated'] as $id) {
            $this->assertNotSame('ENGLISH FALLBACK', $this->text->summary($id, null, 'ENGLISH FALLBACK'), "No locale sentence for summary id {$id}");
        }
    }

    public function test_the_window_is_said_in_words_not_as_a_go_duration(): void
    {
        $out = $this->text->detail([
            'id' => 'log_absent_within',
            'params' => ['pattern' => 'registered with join code', 'within' => '2h0m0s'],
            'detail' => 'nothing matching "registered with join code" in the last 2h0m0s',
        ]);

        $this->assertSame('Nothing matching “registered with join code” in the last 2 hours', $out);
        $this->assertSame('10 minutes', $this->text->duration('10m0s'));
        $this->assertSame('1h 30m', $this->text->duration('1h30m0s'));
        $this->assertSame('45 seconds', $this->text->duration('45s'));
        $this->assertNull($this->text->duration('soon'));
    }

    /** An agent older than protocol 2 sends English only. */
    public function test_an_older_agent_without_ids_shows_its_english(): void
    {
        $this->assertSame('nothing accepting on TCP 25565', $this->text->detail(['detail' => 'nothing accepting on TCP 25565']));
        $this->assertSame('not running', $this->text->summary(null, null, 'not running'));
    }

    /** An agent NEWER than this forum sends an ID the locale has never heard of. */
    public function test_an_unknown_id_from_a_newer_agent_shows_its_english_not_a_key(): void
    {
        $out = $this->text->detail(['detail' => 'disk 92% full on /srv', 'id' => 'disk_full', 'params' => ['percent' => 92]]);
        $this->assertSame('disk 92% full on /srv', $out);

        $this->assertSame('a brand new state', $this->text->summary('brand_new', null, 'a brand new state'));
    }

    /** The probe name is the operator's own words and is shown as written. */
    public function test_a_failing_probe_summary_is_the_probe_name(): void
    {
        $this->assertSame(
            'matchmaking threw and never recovered',
            $this->text->summary('probe_failed', ['probe' => 'matchmaking threw and never recovered'], 'matchmaking threw and never recovered')
        );
        $this->assertSame('fallback', $this->text->summary('probe_failed', [], 'fallback'));
    }

    /**
     * 🚨 The id comes off the wire from a host the forum does not control. One
     * that is not shaped like an id must never be spliced into a locale key.
     */
    public function test_an_id_that_is_not_an_id_is_never_used_as_a_key(): void
    {
        $this->assertSame('x', $this->text->detail(['detail' => 'x', 'id' => '../forum.state.running']));
        $this->assertSame('x', $this->text->summary('summary.not_running', null, 'x'));
    }

    /** Params that are not scalars are dropped, not stringified into "Array". */
    public function test_non_scalar_params_are_ignored(): void
    {
        $out = $this->text->detail(['detail' => 'x', 'id' => 'log_found', 'params' => ['pattern' => ['nested']]]);
        $this->assertStringNotContainsString('Array', $out);
    }
}
