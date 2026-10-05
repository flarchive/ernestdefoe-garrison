<?php

/*
 * Garrison — run your game servers from the forum the players already live in.
 */

use ErnestDefoe\Garrison\Api\Controller\AdminController;
use ErnestDefoe\Garrison\Api\Controller\AgentPollController;
use ErnestDefoe\Garrison\Api\Controller\CommandStatusController;
use ErnestDefoe\Garrison\Api\Controller\ConsoleController;
use ErnestDefoe\Garrison\Api\Controller\ListServersController;
use ErnestDefoe\Garrison\Api\Controller\QueueCommandController;
use ErnestDefoe\Garrison\Api\Resource\ServerResource;
use ErnestDefoe\Garrison\Console\HealthCommand;
use ErnestDefoe\Garrison\Console\PairCommand;
use ErnestDefoe\Garrison\GarrisonServiceProvider;
use ErnestDefoe\Garrison\Notification\ServerIncidentBlueprint;
use Flarum\Extend;

$extenders = [
    (new Extend\ServiceProvider())->register(GarrisonServiceProvider::class),

    (new Extend\Locales(__DIR__ . '/resources/locale')),

    (new Extend\Frontend('admin'))
        ->js(__DIR__ . '/js/dist/admin.js')
        ->css(__DIR__ . '/less/admin.less'),

    (new Extend\Frontend('forum'))
        ->js(__DIR__ . '/js/dist/forum.js')
        ->css(__DIR__ . '/less/forum.less')

        /*
         * 🚨 The status page needs registering on the PHP side as well as in
         * the JS router. Without this, clicking through to /garrison inside
         * the app works — Mithril handles it client-side — and loading the
         * same URL directly, or refreshing on it, or following a link anybody
         * shared, returns a bare 404 from the server, because nothing there
         * knows to serve the forum shell for that path.
         *
         * The kind of bug that never shows up while you are developing,
         * because you always arrive by clicking.
         */
        ->route('/garrison', 'garrison')

        /*
         * 🚨 And the per-server page, for the same reason and more sharply.
         *
         * Every alert Garrison sends links here. An email arriving at 3am, a
         * notification, a link pasted into a staff channel — all of them are
         * DIRECT loads, which is exactly the case a client-side route does not
         * cover. Missing this would 404 the one path the product's own
         * notifications take.
         */
        ->route('/garrison/s/{id}', 'garrison.server'),

    (new Extend\Console())
        ->command(PairCommand::class)
        ->command(HealthCommand::class)

        /*
         * 🚨 Every minute, not every five. The whole argument for this feature
         * is the gap between a server breaking and somebody noticing — on the
         * outage that prompted it, twenty hours. A ladder that needs three
         * consecutive unready readings before acting already waits three
         * minutes; a five-minute schedule would make that fifteen.
         */
        ->schedule(HealthCommand::class, fn ($event) => $event->everyMinute()->withoutOverlapping()),

    /*
     * 🚨 The agent's route is exempt from CSRF, through core's own extender.
     *
     * CSRF protects a BROWSER session from being driven by another site. An
     * agent has no session and no cookie to ride on — it presents a bearer
     * token and nothing else — so there is no cross-site request to forge and
     * the check can only ever reject it. Predicted in AgentPollController's
     * docblock and still shipped broken: the first live agent polled for two
     * minutes getting `csrf_token_mismatch` every time.
     *
     * Exempting ONE named route, not removing the middleware. Removing it
     * would disarm CSRF for the whole forum to fix one endpoint.
     */
    (new Extend\Csrf())->exemptRoute('garrison.agent.poll'),

    (new Extend\Routes('api'))
        /*
         * 🚨 The agent route is NOT an api resource and does not sit behind
         * the machinery built for browsers. An agent has no session, no cookie
         * and no CSRF token; routing it through that would mean either
         * weakening it or teaching the agent to pretend to be a browser.
         */
        ->post('/garrison/agent/poll', 'garrison.agent.poll', AgentPollController::class)

        ->get('/garrison/servers', 'garrison.servers', ListServersController::class)
        ->post('/garrison/servers/{id}/command', 'garrison.command', QueueCommandController::class)
        ->get('/garrison/servers/{id}/console', 'garrison.console', ConsoleController::class)

        /*
         * 🚨 Readable, because a 202 is not an outcome. Restoring a world is
         * the most consequential thing this product does and it would
         * otherwise be the action with the least feedback — see the
         * controller.
         */
        ->get('/garrison/commands/{id}', 'garrison.command.status', CommandStatusController::class)

        /*
         * 🚨 Reachable by ordinary members, which nothing else here is.
         *
         * Proving who you are in a game is something a player does, not
         * something staff do for them. The controller acts only on the
         * actor's own identity and takes no user id from the request — see
         * its docblock — and the console line it ultimately causes is
         * rendered by the AGENT from the operator's template, never composed
         * here.
         */

        /*
         * 🚨 Endpoints of their own rather than attributes on the user
         * resource. Playtime hung off Flarum's user serializer would be a
         * query per user in every payload, and a discussion page serializes
         * dozens — the request-per-rendered-item shape that once exhausted a
         * database connection cap and 500'd a whole forum.
         */

        /*
         * Admin. Every one of these resolves the same controller, which
         * asserts `garrison.manage` before it looks at the route name — so
         * there is no path into any of them that skips the check.
         */
        ->get('/garrison/admin/state', 'garrison.admin.state', AdminController::class)
        ->post('/garrison/admin/agents', 'garrison.admin.pair', AdminController::class)
        ->delete('/garrison/admin/agents/{id}', 'garrison.admin.unpair', AdminController::class)
        ->patch('/garrison/admin/servers/{id}', 'garrison.admin.server', AdminController::class)
        ->post('/garrison/admin/servers/{id}/icon', 'garrison.admin.icon', AdminController::class)
        ->post('/garrison/admin/servers/{id}/fetch-icon', 'garrison.admin.fetchIcon', AdminController::class),

    /*
     * Four permissions, and console is separate from control on purpose.
     *
     * 🚨 Restarting a server is an operational act. Sending a line to a game
     * console is arbitrary in-game authority — op, ban, give, teleport — and
     * an operator may very reasonably want somebody who can do the first and
     * not the second. Folding them together is a decision that cannot be
     * undone by configuration.
     */
    (new Extend\Policy()),

    /*
     * 🚨 REGISTERED SO THAT NOTIFICATIONS CAN SERIALIZE — not so that anybody
     * can fetch a server through it (it declares no endpoints).
     *
     * Flarum builds the notifications endpoint's `subject` relationship over
     * every blueprint's subject model, and a model with no registered resource
     * contributes a NULL to that list. The null is inert until something
     * resolves the relationship, and then /api/notifications 500s — for every
     * user on the forum, not just Garrison's. See ServerResource's docblock;
     * this same bug shipped once already in another extension.
     */
    (new Extend\ApiResource(ServerResource::class)),

    /*
     * 🚨 The email templates live under a NAMESPACE, and the blueprint names
     * that namespace in getEmailViews(). A missing View extender is a "view
     * not found" thrown inside the queued mail job — which surfaces as a
     * failed job in a queue nobody is watching, and as an alert that simply
     * never arrives.
     */
    (new Extend\View())
        ->namespace('ernestdefoe-garrison', __DIR__ . '/views'),

    /*
     * 🚨 The blueprint must ALSO implement AlertableInterface and
     * MailableInterface, which this extender cannot enforce. Without the
     * first, Flarum's alert driver registers no preference default,
     * NotificationSyncer filters out every recipient, and nothing is ever
     * delivered — silently. Without the second, the email column in a user's
     * notification preferences is disabled and this list is a lie.
     *
     * 🚨 BOTH DRIVERS ON BY DEFAULT, deliberately.
     *
     * The tempting default is alert-only, on the grounds that email is
     * intrusive. But this notification goes to people who hold
     * `garrison.manage` — staff who asked to be responsible for these servers
     * — and the entire argument for the product is the gap between a server
     * breaking and somebody noticing. An alert that waits in a dropdown until
     * the next time somebody opens the forum reproduces exactly the failure
     * being fixed. Anybody who disagrees has a checkbox.
     */
    (new Extend\Notification())
        ->type(ServerIncidentBlueprint::class, ['alert', 'email']),

    (new Extend\Settings())
        ->serializeToForum('garrisonWebhookConfigured', 'ernestdefoe-garrison.webhook_url', fn ($v) => ! empty($v)),
];

/*
 * 🚨 Widget hosts are OPTIONAL COLLABORATORS, registered conditionally.
 *
 * Garrison must work with none of the four installed and with all four
 * installed. The JS side resolves fof/forum-widgets-core and Bespoke through
 * their own runtime registries; only Page Builder needs a PHP extender,
 * because it is the one whose contract has a server half.
 *
 * class_exists rather than an extension-status lookup: this file is evaluated
 * before the extension manager is usable, and a hard reference to a class that
 * may not be installed is a fatal at boot for everybody.
 */
if (class_exists(\Ernestdefoe\PageBuilder\Extend\PageBuilderBlock::class)) {
    $extenders[] = new \Ernestdefoe\PageBuilder\Extend\PageBuilderBlock(
        \ErnestDefoe\Garrison\Widget\ServerStatusBlock::class
    );
}

return $extenders;
