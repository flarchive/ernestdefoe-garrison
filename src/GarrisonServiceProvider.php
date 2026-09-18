<?php

namespace ErnestDefoe\Garrison;

use ErnestDefoe\Garrison\Agent\Dispatcher;
use ErnestDefoe\Garrison\Agent\Gateway;
use ErnestDefoe\Garrison\Agent\TokenGuard;
use ErnestDefoe\Garrison\Api\Controller\AdminController;
use ErnestDefoe\Garrison\Api\Controller\ConsoleController;
use ErnestDefoe\Garrison\Entitled;
use ErnestDefoe\Garrison\Game\Artwork;
use ErnestDefoe\Garrison\Health\Ladder;
use ErnestDefoe\Garrison\Notification\Alerts;
use ErnestDefoe\Garrison\Health\Heartbeat;
use ErnestDefoe\Garrison\Notification\Webhooks;
use ErnestDefoe\Garrison\Players\Tracker;
use Flarum\Foundation\AbstractServiceProvider;
use Flarum\Locale\TranslatorInterface;
use Illuminate\Contracts\Filesystem\Factory;
use Illuminate\Contracts\Queue\Queue;
use Flarum\Notification\NotificationSyncer;
use Flarum\Settings\SettingsRepositoryInterface;
use Illuminate\Database\ConnectionInterface;
use Psr\Log\LoggerInterface;

/**
 * 🚨 Every collaborator is REQUIRED, never optional.
 *
 * A constructor taking `?Foo $foo = null` is indistinguishable, at runtime,
 * between "nobody supplies it" and "it is not needed" — and a safety net that
 * is silently absent is worse than one that was never built, because the UI
 * still shows it. Millwright's automatic rollback was disconnected on every
 * install in the world for exactly this reason while its unit test passed.
 * Required dependencies turn the same slip into a TypeError at boot, where
 * somebody sees it.
 */
class GarrisonServiceProvider extends AbstractServiceProvider
{
    public function register(): void
    {
        $this->container->singleton(TokenGuard::class, function () {
            return new TokenGuard();
        });

        $this->container->singleton(Tracker::class, function () {
            return new Tracker();
        });


        $this->container->singleton(Gateway::class, function ($container) {
            return new Gateway(
                $container->make(ConnectionInterface::class),
                $container->make(Tracker::class)
            );
        });

        $this->container->singleton(Entitled::class, function ($container) {
            return new Entitled($container->make(SettingsRepositoryInterface::class));
        });

        $this->container->singleton(Dispatcher::class, function ($container) {
            return new Dispatcher(
                $container->make(TranslatorInterface::class),
                $container->make(Entitled::class)
            );
        });

        $this->container->singleton(Webhooks::class, function ($container) {
            return new Webhooks(
                $container->make(SettingsRepositoryInterface::class),
                $container->make(LoggerInterface::class),
                $container->make(TranslatorInterface::class),
                $container->make(\Flarum\Http\UrlGenerator::class)
            );
        });

        $this->container->singleton(Alerts::class, function ($container) {
            return new Alerts(
                $container->make(NotificationSyncer::class),
                $container->make(LoggerInterface::class),
                $container->make(Webhooks::class)
            );
        });

        /*
         * 🚨 The queue is resolved when Heartbeat is built, not captured
         * at boot. Core's own EmailNotificationDriver has a comment explaining
         * why: the RoutingQueue wrapper that puts jobs on their registered
         * queue is applied in QueueServiceProvider::boot, AFTER extension
         * service providers register. Grabbing the connection too early gets
         * the unwrapped driver and silently bypasses routing — which for a
         * heartbeat would mean measuring a queue nothing else uses.
         */
        $this->container->singleton(Heartbeat::class, function ($container) {
            return new Heartbeat(
                $container->make(Queue::class),
                $container->make(SettingsRepositoryInterface::class)
            );
        });


        $this->container->singleton(Ladder::class, function ($container) {
            return new Ladder(
                $container->make(Dispatcher::class),
                $container->make(Alerts::class)
            );
        });

        $this->container->singleton(ConsoleController::class, function ($container) {
            return new ConsoleController($container->make(ConnectionInterface::class));
        });

        $this->container->singleton(Artwork::class, function ($container) {
            return new Artwork($container->make(Factory::class));
        });

        $this->container->singleton(AdminController::class, function ($container) {
            return new AdminController(
                $container->make(TranslatorInterface::class),
                $container->make(Factory::class),
                $container->make(Artwork::class),
                $container->make(Heartbeat::class)
            );
        });
    }
}
