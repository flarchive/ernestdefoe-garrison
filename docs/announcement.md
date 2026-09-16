<!--
  The discuss.flarum.org announcement, kept with the code.

  Thread title — the "(Built using AI)" is a standing rule and goes in the
  TITLE, not the body:

    Garrison — run your game servers from your forum (Built using AI)

  🚨 One gate left before this is true: `ernestdefoe/garrison` has to be
  submitted to Packagist. Until it is, the free install line in this post
  resolves to nothing. Everything else it describes is live.
-->

# Garrison — run your game servers from your forum

A server can be up, healthy by every measure you have, and quietly refusing to
let anybody in. Mine was, for twenty hours, and nothing told me — the process was
running, the port answered, the panel was green. Somebody eventually mentioned it
on the forum.

Garrison is the thing I wanted that day. It runs your game servers from inside
the forum your players already use, and it is built around noticing that
particular kind of failure rather than the obvious kind.

## What it does

**Start, stop, restart** — from the forum, with the audit trail that implies.

**A console you can read and type into.** Output is retained, so whoever arrives
*after* something went wrong can still read what happened.

**Who is playing, by name.** Garrison reads joins and leaves from the game's own
log — presets for Minecraft, Valheim, ARK, Rust, Terraria and Factorio — so the
forum can show who is on right now. "alice, bob and two others are on" is a
reason to go and join them, and it is the thing a forum can do that a standalone
panel cannot.

**A page for every server, at a URL you can link to.** `/garrison/s/3` is an
ordinary Flarum route using your theme, your permissions and your login. Every
alert links straight to it, because "Shattered Pact stopped accepting players"
followed by a list of eleven servers is a search task at the worst possible
moment.

**It tells you when it is not working.** Garrison reports whether its own
scheduler and queue worker are actually running, because a forum with no
scheduler runs none of this and looks entirely normal.

**Any server you can install** — with or without Docker. A small Go agent runs on
the game host; the forum never touches your machine directly.

---

## Free — `ernestdefoe/garrison`, MIT

**One host, one server.** Lifecycle, console, the status widget, the server page,
and the health alerting. It is a real product, not a trial, and it does not
expire.

```
composer require ernestdefoe/garrison
php flarum cache:clear
```

- Source: <https://github.com/ernestdefoe/garrison>
- Packagist: <https://packagist.org/packages/ernestdefoe/garrison>

The alerting is deliberately in the free tier. A version that cannot tell you
your server has stopped accepting players would be an advert rather than a
product, and that failure is the entire reason this exists.

## Paid — `ernestdefoe/garrison-pro`, commercial licence

**$99/year or $12/month**, adding:

- **Backups you can actually restore from.** Create, list, restore, delete — and
  a **safety copy is taken automatically before every restore**, so the most
  dangerous button in the product is reversible.
- **Copies somewhere else.** Off the game host to S3-compatible storage, because
  a backup on the machine that died is not a backup.
- **Scheduled work.** Nightly restarts, backups and messages to players, with
  advance warnings — which are the difference between a restart and an outage.
  Day toggles and a clock, not a cron field.
- **Settings without a file manager.** Edit `server.properties` and friends from
  the forum — the files you choose, and optionally only the keys you choose, so a
  moderator can change the message of the day without being three lines from the
  RCON password. Comments in the file become the help text, and editing rewrites
  one line in place so your annotated config stays annotated.
- **Player identity, proved in the game.** A player claims a character, Garrison
  whispers them a code **inside the game**, they type it on the forum. Their
  playtime then appears on their profile and on the server's leaderboard. A
  leaderboard of in-game names is something any panel can show; one where the
  names are people you can reply to is not.
- **Unlimited hosts and servers.**

Buy it at <https://ernestdefoe.online/account>. After purchase:

```
composer config repositories.ernestdefoe composer https://ernestdefoe.online/composer
composer config --auth http-basic.ernestdefoe.online token YOUR_TOKEN
composer require ernestdefoe/garrison-pro
php flarum cache:clear
```

From then on it behaves like Packagist — `composer update` and Extension
Manager's update check both see new versions as soon as they ship.

Removing the paid package never deletes anything. Backups already taken stay on
disk and stay listed, schedules pause rather than vanish, identity links stay
linked, and **a running server is never stopped by anything to do with
licensing**.

---

## Requirements

- Flarum **2.0**
- PHP **8.3+**
- A game host you can run a small binary on (Linux x86-64 or arm64)
- Docker **optional**

No other Flarum extension is required. The status pages are ordinary Flarum
routes — there is no page builder, no CMS and no template to assemble first.

## Licences

Two packages, two licences, deliberately:

| Package | Licence |
|---|---|
| `ernestdefoe/garrison` | **MIT** — free tier, use it, fork it, keep it |
| `ernestdefoe/garrison-pro` | Commercial — one licence per forum |

The free package is MIT with no strings and no phone-home. There is **no licence
key anywhere in Garrison**: the paid features live in the paid package, and that
is the whole of the mechanism.

## Support

- Support forum: <https://ernestdefoe.online/t/garrison>
- Issues for the free package: <https://github.com/ernestdefoe/garrison/issues>
- Or reply here — I read this thread.
