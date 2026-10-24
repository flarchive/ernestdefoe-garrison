# Widget hosts

Garrison's server list renders in four places, and none of them is a build-time
dependency — every host is resolved at runtime through its own registry, so the
bundle is identical whether an operator has all four installed or none.

| host | how | verified |
|---|---|---|
| Flarum's own sidebar | `IndexSidebar` extender | yes |
| fof/forum-widgets-core | its `Widgets` extender | yes |
| Bespoke | `window.BespokeWidgetQueue` | yes |
| Page Builder | `window.PageBuilderBlockQueue` + a PHP block | yes |

🚨 **Page Builder is the only one with a server half**, and that turns out to be
the useful one: `ServerStatusBlock::resolve()` is where "may this actor see the
join code" belongs, so the gate is written once and cannot be open on one
surface and shut on another. Verified by placing the block on a real page: a
guest gets name, state and player counts; an admin additionally gets the join
address, password and code — with the block's own `showJoin` setting on in both
cases, because a layout choice must not be able to widen who can see a password.

⚠️ **fof/pages and Page Builder both claim `/p/`.** With both enabled, every
Page Builder page 404s. That is not Garrison's doing and Garrison cannot fix it,
but it is the first thing to check if the block appears to do nothing.

