<?php

namespace ErnestDefoe\Garrison\Agent;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Edition;
use ErnestDefoe\Garrison\Entitled;
use ErnestDefoe\Garrison\Model\Command;
use ErnestDefoe\Garrison\Model\Server;
use Flarum\Foundation\ValidationException;
use Flarum\Locale\TranslatorInterface;
use Flarum\User\User;

/**
 * Queues a command for an agent, having first decided whether it may be asked
 * for at all.
 *
 * 🚨 This class is the forum's half of the security boundary and it repeats
 * the agent's verb set on purpose.
 *
 * The agent refuses anything outside its closed set no matter what reaches it —
 * that is the guarantee, and it does not depend on this file being correct.
 * But a forum that will happily queue `shell.exec` and let the agent reject it
 * has already lost something worth keeping: the audit log fills with attempts
 * nobody can distinguish from bugs, and the UI can offer controls that cannot
 * work. Refusing here too means the two halves agree, and a disagreement
 * between them is a signal rather than noise.
 */
class Dispatcher
{
    /**
     * Verbs the FORUM may queue. Deliberately a subset of what the agent
     * implements: agent.ping and agent.info are the agent's own housekeeping
     * and are not things a person clicks.
     */
    /**
     * Every verb the agent understands — the CATALOGUE, not the permission.
     *
     * 🚨 Which of these a given install may actually queue is `Edition::verbs()`,
     * because half of them belong to garrison-pro. This list stays complete so
     * that an unknown verb and an unlicensed one give different answers: one is
     * a typo, the other is an upgrade, and telling a customer "unknown verb"
     * when they have hit the edge of their tier is a support ticket about
     * nothing.
     *
     * `EditionTest` asserts that FREE_VERBS and PRO_VERBS partition this list
     * exactly, so a verb added here can never quietly belong to neither tier.
     */
    public const QUEUEABLE = [
        'server.status',
        'server.start',
        'server.stop',
        'server.restart',
        'server.stats',
        'console.tail',
        'console.send',
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

    /**
     * Verbs that change the world, as opposed to reading it. They need a
     * heavier permission and they are the ones the audit log exists for.
     */
    public const MUTATING = [
        'server.start',
        'server.stop',
        'server.restart',
        'console.send',
        'backup.create',
        'config.set',
        'provision.install',
    ];

    /**
     * Verbs that can destroy data, and need `garrison.manage`.
     *
     * 🚨 Separated from MUTATING deliberately. Restarting a server inconveniences
     * people for a minute; restoring the wrong backup or deleting the right one
     * loses work that cannot be recovered. Somebody trusted to keep a server
     * running is not automatically somebody trusted to overwrite its world, and
     * folding the two together is a decision an operator could never undo
     * through configuration.
     */
    public const DESTRUCTIVE = [
        'backup.restore',
        'backup.delete',
    ];

    public function __construct(
        protected TranslatorInterface $translator,
        protected Entitled $entitled
    ) {
    }

    /**
     * Queue one command, or throw.
     */
    public function queue(User $actor, Server $server, string $verb, array $params = [], string $source = 'user'): Command
    {
        if (! in_array($verb, self::QUEUEABLE, true)) {
            // 🚨 Not an assertion or a 500. A verb the forum does not queue is
            // a refusal with a reason, because the same path is reachable from
            // the API by anybody with an account.
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.unknown_verb'),
            ]);
        }

        /*
         * 🚨 A real verb this edition does not have, answered as ITS OWN thing.
         *
         * Not "unknown verb", which would send somebody hunting for a typo in
         * a word that is spelled correctly, and not "not permitted", which
         * reads as a permission an administrator could go and grant. This is
         * the only refusal in the product that no setting on this forum can
         * lift, so it says so.
         */
        if (! in_array($verb, Edition::verbs(), true)) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.needs_pro'),
            ]);
        }

        /*
         * 🚨 And the SERVER has to be one this edition covers.
         *
         * Checked here beside the verb, above every permission branch, because
         * an administrator on the free tier still holds `garrison.manage` —
         * a cap enforced below that shortcut would never run for the one person
         * most likely to own a second server. That is the same ordering
         * mistake `DispatcherOrderTest` already exists to catch, and it reads
         * as a broken feature rather than as an unenforced limit.
         */
        if (! $this->entitled->allowsServer((int) $server->id)) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.needs_pro'),
            ]);
        }

        $this->assertPermitted($actor, $server, $verb);

        $command = new Command();
        $command->agent_id = $server->agent_id;
        $command->server_ref = $server->ref;
        $command->verb = $verb;
        $command->params = $params === [] ? null : json_encode($params);
        $command->status = 'queued';
        $command->actor_id = $actor->id;
        $command->source = $source;
        $command->created_at = Carbon::now();
        $command->save();

        return $command;
    }

    /**
     * 🚨 Written as one method, called from one place, so there is no surface
     * that can queue a command without passing through it. The commonest bug
     * of this kind is a second call site added later that forgets the check —
     * and it is invisible, because the feature works.
     */
    protected function assertPermitted(User $actor, Server $server, string $verb): void
    {
        /*
         * 🚨 player.verify is the ONE verb ordinary members may queue, and it
         * is checked first because every rule below would refuse it.
         *
         * It is safe for them for a reason that lives on the other side: the
         * agent renders the whisper from the OPERATOR's template and refuses
         * any player it cannot currently see in the game, so the forum can
         * neither compose console text nor aim it at somebody who is not
         * there. Without that, this would be handing every account the ban and
         * op commands — see players.VerifyLine.
         *
         * The gate that remains is visibility: you may prove who you are on a
         * server you can see. On one you cannot, the answer is the same 404
         * the rest of the product gives, because otherwise this verb becomes a
         * way to discover that a private server exists.
         */
        if ($verb === 'player.verify') {
            $actor->assertRegistered();

            if (! $server->is_public && ! $actor->hasPermission('garrison.view')) {
                throw new ValidationException([
                    'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
                ]);
            }

            return;
        }

        if ($actor->hasPermission('garrison.manage')) {
            return;
        }

        /*
         * 🚨 Reaching here means the actor does NOT have `garrison.manage`,
         * and provisioning needs exactly that and nothing less.
         *
         * Installing a server writes to the host's disk, downloads gigabytes
         * over its connection, and adds a server the operator did not
         * personally create. Every one of those is something a person trusted
         * to restart a server is not automatically trusted to do — and unlike
         * a restart, none of them is undone by pressing the other button.
         *
         * 🚨 Placed AFTER the manage check on purpose. The first version of
         * this sat above it and refused everybody including administrators,
         * because every branch in this method runs on the path where manage is
         * absent. An ordering mistake in a permission check reads as a broken
         * feature rather than as a security bug, which is how it survives.
         */
        if (str_starts_with($verb, 'provision.')) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
            ]);
        }

        // 🚨 Destructive verbs have no permission below manage. There is
        // deliberately no `garrison.restore` to grant: an operator who wants
        // somebody restoring worlds is giving them the panel.
        if (in_array($verb, self::DESTRUCTIVE, true)) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
            ]);
        }

        $needed = in_array($verb, self::MUTATING, true)
            ? 'garrison.control'
            : 'garrison.view';

        if (! $actor->hasPermission($needed)) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
            ]);
        }

        // console.send deserves its own gate rather than riding along with
        // restart. Sending a line to a game console is arbitrary in-game
        // authority — ban, op, give items — and an operator may reasonably
        // want somebody who can restart a server but not do that.
        if ($verb === 'console.send' && ! $actor->hasPermission('garrison.console')) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
            ]);
        }

        /*
         * 🚨 And configuration gets its own gate, for the same reason again.
         *
         * Changing the message of the day and restarting a server are
         * unrelated kinds of trust. The agent already constrains WHICH files
         * and which keys are reachable, so this is not the only protection —
         * but a community that wants a moderator updating the MOTD should not
         * have to hand them the ability to restart during a raid, and the
         * reverse is just as true.
         *
         * Reading is gated too, not only writing: a config file an operator
         * declared read-only is still their server's internals, and
         * `garrison.view` means "see the servers", not "see inside them".
         */
        if (str_starts_with($verb, 'config.') && ! $actor->hasPermission('garrison.config')) {
            throw new ValidationException([
                'verb' => $this->translator->trans('ernestdefoe-garrison.api.errors.not_permitted'),
            ]);
        }
    }

    /**
     * Commands that were delivered but never answered.
     *
     * An agent that takes a command and then dies leaves a row that would
     * otherwise say "delivered" for ever, which reads as "in progress" on a
     * dashboard and makes an operator wait for something that will never
     * happen. Called from the scheduler.
     */
    public function expireStale(int $olderThanSeconds = 300): int
    {
        return Command::query()
            ->where('status', 'delivered')
            ->where('delivered_at', '<', Carbon::now()->subSeconds($olderThanSeconds))
            ->update([
                'status' => 'expired',
                'error_code' => 'timeout',
                'completed_at' => Carbon::now(),
            ]);
    }
}
