{{--
  🚨 No string transforms on trans() output in here — not mb_strtoupper, not
  ucfirst, nothing. Flarum swaps every translation PARAMETER for an opaque
  marker while the mail renders and restores it afterwards, and the restore
  matches case-sensitively. Uppercasing a translated line therefore delivers
  `FLARUMSAFEVALUE…ENDFLARUMSAFEVALUE` to somebody's inbox, and the detector
  meant to catch that is case-sensitive too, so nothing warns. It has shipped
  once, on another extension, to real subscribers.
--}}
<x-mail::plain.notification>
<x-slot:body>
{!! $translator->trans('ernestdefoe-garrison.email.server_incident.body.' . $blueprint->key(), [
'name' => $blueprint->server->name,
'url' => $url->to('forum')->route('garrison.server', ['id' => $blueprint->server->id]),
]) !!}
@if ($blueprint->summary)

{!! $translator->trans('ernestdefoe-garrison.email.server_incident.body.finding', [
'finding' => $blueprint->summary,
]) !!}
@endif
</x-slot:body>
</x-mail::plain.notification>
