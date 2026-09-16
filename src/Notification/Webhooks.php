<?php

namespace ErnestDefoe\Garrison\Notification;

use ErnestDefoe\Garrison\Model\Server;
use Flarum\Http\UrlGenerator;
use Flarum\Locale\TranslatorInterface;
use Flarum\Settings\SettingsRepositoryInterface;
use Psr\Log\LoggerInterface;

/**
 * Posts incidents to Discord, or to any URL that accepts JSON.
 *
 * 🚨 Where the community actually is. A Flarum notification reaches somebody
 * who opens the forum; an outage at 3am is noticed by whoever has Discord on
 * their phone, and that is the difference between a twenty-minute outage and a
 * twenty-hour one.
 */
class Webhooks
{
    public function __construct(
        protected SettingsRepositoryInterface $settings,
        protected LoggerInterface $log,
        protected TranslatorInterface $translator,
        protected UrlGenerator $url
    ) {
    }

    public function serverIncident(Server $server, string $state, ?string $summary): void
    {
        $url = trim((string) $this->settings->get('ernestdefoe-garrison.webhook_url'));

        if ($url === '') {
            return;
        }

        $text = $this->line($server, $state, $summary);

        /*
         * Discord's own shape when it is a Discord URL, a plain envelope
         * otherwise. Detected from the host rather than asking the operator to
         * tell us which it is — a dropdown they can set wrongly is a support
         * thread waiting to happen.
         */
        $body = str_contains($url, 'discord.com') || str_contains($url, 'discordapp.com')
            ? ['content' => $text]
            : [
                'event' => 'server.' . $state,
                'server' => $server->name,
                'serverId' => $server->id,
                'summary' => $summary,
                'text' => $text,
            ];

        $this->post($url, $body);
    }

    /**
     * 🚨 TRANSLATED, not built from English string literals here.
     *
     * These lines used to be four hardcoded sentences in this method, which
     * made a German community's Discord channel the one place in the whole
     * product that spoke English at them — and the place they would see most
     * often, because it is the one that reaches a phone.
     *
     * The FORUM's locale, not a user's: a webhook has no recipient to have a
     * preference. `trans()` without an actor uses the default, which is the
     * right answer and also the only available one.
     */
    protected function line(Server $server, string $state, ?string $summary): string
    {
        $key = match ($state) {
            'unready', 'down', 'recovered', 'abandoned' => $state,
            // 🚨 A closed set with a fallback, for the third time in this
            // codebase. An unexpected state must never compose a translation
            // key that does not exist — here that would post a raw key into
            // somebody's Discord, where it cannot be edited afterwards.
            default => 'unknown',
        };

        return $this->translator->trans('ernestdefoe-garrison.webhook.' . $key, [
            'name' => $server->name,
            'state' => $state,

            /*
             * 🚨 A LINK, because the whole point of this channel is that it
             * reaches somebody who is not looking at the forum. Telling them a
             * server is down and leaving them to go and find it is most of the
             * delay this feature exists to remove — and it goes to the
             * server's own page, not the list, for the same reason the alert
             * emails do.
             */
            'url' => $this->url->to('forum')->route('garrison.server', ['id' => $server->id]),
        ]) . ($summary && in_array($state, ['unready', 'down'], true)
            ? ' — ' . $summary
            : '');
    }

    protected function post(string $url, array $body): void
    {
        $payload = json_encode($body);

        $context = stream_context_create([
            'http' => [
                'method' => 'POST',
                'header' => "Content-Type: application/json\r\nUser-Agent: Garrison/1.0\r\n",
                'content' => $payload,
                // 🚨 Short, and failures are swallowed below. A webhook is a
                // courtesy: an unreachable Discord must never hold up the
                // scheduled tick that is trying to FIX the server, which is
                // the actually important thing happening at that moment.
                'timeout' => 5,
                'ignore_errors' => true,
            ],
        ]);

        $result = @file_get_contents($url, false, $context);

        if ($result === false) {
            // Logged, not thrown. Nothing about a failed notification should
            // stop remediation.
            $this->log->info('garrison: webhook post failed');
        }
    }
}
