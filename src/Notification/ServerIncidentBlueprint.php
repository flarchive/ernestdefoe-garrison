<?php

namespace ErnestDefoe\Garrison\Notification;

use ErnestDefoe\Garrison\Model\Server;
use Flarum\Database\AbstractModel;
use Flarum\Locale\TranslatorInterface;
use Flarum\Notification\AlertableInterface;
use Flarum\Notification\Blueprint\BlueprintInterface;
use Flarum\Notification\MailableInterface;
use Flarum\User\User;

/**
 * "A server stopped accepting players", and what happened next.
 *
 * 🚨 IMPLEMENTS AlertableInterface, and that marker is not optional.
 *
 * Flarum 2's alert driver silently ignores any blueprint without it: no
 * preference default is registered, so `getPreference('notify_<type>_alert')`
 * returns NULL, `NotificationSyncer::sync()` filters out every recipient, and
 * `send()` no-ops. Nothing errors. The feature simply never delivers anything
 * to anybody, and the only symptom is an absence — which has shipped before,
 * in another extension, and took a boot probe to find.
 */
class ServerIncidentBlueprint implements BlueprintInterface, AlertableInterface, MailableInterface
{
    /**
     * 🚨 Public, because the email templates read them.
     *
     * A blade view renders with `$blueprint` in scope and reaches straight
     * through it — `$blueprint->server->name`. Protected properties there fail
     * at SEND time, inside the queued job, where the only trace is a failed
     * job nobody is looking at. Readonly so that being reachable from a
     * template does not make them writable from one.
     */
    public function __construct(
        public readonly Server $server,
        public readonly string $state,
        public readonly ?string $summary = null
    ) {
    }

    public function getFromUser(): ?User
    {
        // 🚨 Nobody. A health incident was not caused by a person, and
        // attributing it to one would put a member's name and avatar on "your
        // server is down" — which reads as an accusation.
        return null;
    }

    public function getSubject(): ?AbstractModel
    {
        return $this->server;
    }

    public function getData(): mixed
    {
        return [
            'name' => $this->server->name,
            'state' => $this->state,
            'summary' => $this->summary,
        ];
    }

    public function getEmailViews(): array
    {
        /*
         * 🚨 BOTH, always. Flarum's mailer asks for `text` and `html` by name
         * and throws when either is missing — at send time, in the queue, long
         * after anything is watching. One view is not a degraded email; it is
         * a failed job.
         *
         * One pair of templates for all four states rather than four pairs:
         * the states differ by one sentence, and four near-identical templates
         * is four places for a copy fix to be applied in three.
         */
        return [
            'text' => 'ernestdefoe-garrison::emails.plain.serverIncident',
            'html' => 'ernestdefoe-garrison::emails.html.serverIncident',
        ];
    }

    public function getEmailSubject(TranslatorInterface $translator): string
    {
        /*
         * 🚨 The server's name goes in the SUBJECT, not just the body.
         *
         * This email's whole job is to be readable from a lock screen at 3am.
         * "A server needs attention" makes somebody open their laptop to learn
         * which one; "Shattered Pact stopped accepting players" does not.
         */
        return $translator->trans(
            'ernestdefoe-garrison.email.server_incident.subject.' . $this->key(),
            ['name' => $this->server->name]
        );
    }

    /**
     * 🚨 The same closed mapping the JS component uses, for the same reason: an
     * unexpected state must not be able to compose a translation key that does
     * not exist. In an email that means a raw key posted to somebody's inbox,
     * where it cannot be taken back.
     */
    public function key(): string
    {
        return match ($this->state) {
            'recovered' => 'recovered',
            'abandoned' => 'abandoned',
            'unready' => 'unready',
            default => 'down',
        };
    }

    public static function getType(): string
    {
        return 'garrisonServerIncident';
    }

    public static function getSubjectModel(): string
    {
        return Server::class;
    }
}
