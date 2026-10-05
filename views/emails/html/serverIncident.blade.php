{{--
  🚨 Through $formatter->convert(), which is Flarum's own post formatter: it
  turns the blank line into paragraphs and the bare URL into a link. The
  translation is TEXT — HTML written into it would be parsed as forum markup
  rather than passed through. Same rule as the plain view: no string
  transforms over trans() output, ever.
--}}
<x-mail::html.notification>
    <x-slot:body>
        {!! $formatter->convert($translator->trans('ernestdefoe-garrison.email.server_incident.body.' . $blueprint->key(), [
            'name' => $blueprint->server->name,
            'url' => $url->to('forum')->route('garrison.server', ['id' => $blueprint->server->id]),
        ])) !!}

        @if ($blueprint->summary)
            {!! $formatter->convert($translator->trans('ernestdefoe-garrison.email.server_incident.body.finding', [
                'finding' => $blueprint->summary,
            ])) !!}
        @endif
    </x-slot:body>
</x-mail::html.notification>
