<?php

namespace ErnestDefoe\Garrison\Tests;

use ErnestDefoe\Garrison\Agent\TokenGuard;
use PHPUnit\Framework\TestCase;

/**
 * Agent tokens: a fast, constant-time check, so an unauthenticated poll that
 * names a real agent id cannot make the forum run bcrypt.
 */
class TokenGuardTest extends TestCase
{
    public function testAMintedTokenIsNotBcrypt(): void
    {
        [$plain, $hash] = TokenGuard::mint(3);

        $this->assertStringStartsWith('3.', $plain);
        $this->assertStringStartsWith('sha256:', $hash);
        $this->assertStringNotContainsString(substr($plain, 2), $hash, 'the secret is stored in the clear');

        $this->assertTrue(TokenGuard::matches(substr($plain, 2), $hash));
        $this->assertFalse(TokenGuard::matches(substr($plain, 2).'x', $hash));
        $this->assertFalse(TokenGuard::matches('', $hash));
    }

    /** Agents paired before the change keep working. */
    public function testAnOlderBcryptHashStillVerifies(): void
    {
        $old = password_hash('s3cret', PASSWORD_DEFAULT);

        $this->assertTrue(TokenGuard::matches('s3cret', $old));
        $this->assertFalse(TokenGuard::matches('wrong', $old));
        $this->assertFalse(TokenGuard::matches('s3cret', ''));
    }
}
