<?php

namespace ErnestDefoe\Garrison;

/**
 * Which edition of Garrison is installed, and therefore what it may do.
 *
 * 🚨 THIS IS THE ONLY PLACE THE FREE/PAID BOUNDARY IS DECIDED. Every gate in
 * the extension asks this class; nothing asks it twice in two different ways,
 * and nothing hides a feature in the browser that this class would allow on the
 * server. A second opinion about entitlement is a second place for the two to
 * disagree, and the one that loses is always the one that hides things.
 *
 * 🚨 There is NO licence key here, and there must never be one.
 *
 * This package is MIT and public: a check inside code the reader is licensed to
 * modify is decoration, not protection. The boundary is possession — the paid
 * features live in `ernestdefoe/garrison-pro`, which is a separate package with
 * a separate licence, and `enablePro()` below is called by that package's
 * service provider. If pro is installed, it is installed; if it is not, this
 * class describes a free tier that is a real product rather than a nag screen.
 *
 * See docs/entitlement.md for why it was built this way rather than as a
 * phone-home, and for what that does and does not protect.
 */
class Edition
{
    /**
     * What the free tier may ask an agent to do.
     *
     * 🚨 Lifecycle, console and status — the three that make a forum able to
     * run a game server at all — plus the health probes underneath them.
     *
     * The probes are deliberately NOT paid. A free tier that cannot tell you
     * your server has stopped accepting players is an advert rather than a
     * product, and that exact failure — up, healthy-looking and unjoinable for
     * twenty hours — is the reason this extension exists. Paywalling it would
     * make the paid tier read as a hostage situation instead of an upgrade.
     */
    public const FREE_VERBS = [
        'server.status',
        'server.start',
        'server.stop',
        'server.restart',
        'server.stats',
        'console.tail',
        'console.send',
    ];

    /**
     * What `garrison-pro` adds.
     *
     * Listed here rather than in pro so that the whole boundary is readable in
     * one file, and so a free install can say WHICH tier a refused verb belongs
     * to instead of only that it was refused.
     */
    public const PRO_VERBS = [
        'backup.create',
        'backup.list',
        'backup.restore',
        'backup.delete',
        'config.list',
        'config.get',
        'config.set',
        'player.verify',
        'provision.templates',
        'provision.install',
    ];

    /** How many paired hosts the free tier allows. */
    public const FREE_HOSTS = 1;

    /** How many servers the free tier shows and manages. */
    public const FREE_SERVERS = 1;

    private static bool $pro = false;

    /**
     * Called by garrison-pro's service provider. Nothing else may call it.
     *
     * 🚨 Deliberately not driven by `class_exists()` or by reading the
     * extension list: an extension that is INSTALLED but DISABLED must count as
     * absent, and only its service provider knows that — Flarum does not boot
     * the providers of disabled extensions. Sniffing for the class would light
     * up the paid features on a forum that had switched them off.
     */
    public static function enablePro(): void
    {
        self::$pro = true;
    }

    public static function isPro(): bool
    {
        return self::$pro;
    }

    /** Every verb this install may queue. */
    public static function verbs(): array
    {
        return self::$pro
            ? [...self::FREE_VERBS, ...self::PRO_VERBS]
            : self::FREE_VERBS;
    }

    /** True when the verb exists but belongs to the tier this install has not got. */
    public static function isProVerb(string $verb): bool
    {
        return in_array($verb, self::PRO_VERBS, true);
    }

    /** Null means unlimited. */
    public static function maxHosts(): ?int
    {
        return self::$pro ? null : self::FREE_HOSTS;
    }

    /** Null means unlimited. */
    public static function maxServers(): ?int
    {
        return self::$pro ? null : self::FREE_SERVERS;
    }

    /**
     * 🚨 Tests only. Static state outlives a single test, and a test that
     * enabled pro would silently grant it to every test that ran afterwards —
     * which would turn the free-tier assertions green without them ever being
     * true.
     */
    public static function resetForTesting(): void
    {
        self::$pro = false;
    }
}
