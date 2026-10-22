# Open core — the free tier, and what the paid package adds

> **Status: BUILT in 1.1.0.** Where this document and the code disagree, the
> code is right and this note says why — three decisions changed while building
> it, all in the direction of less risk.

Garrison sells at **$99/year or $12/month**, above a **free tier of one host and
one server** covering lifecycle, console and the status widget. The free tier is
the funnel: a real, supported product, not a crippled demo and not a trial that
expires.

It is delivered as **two packages**, decided 2026-09-15:

| Package | Licence | Delivery | Is |
|---|---|---|---|
| `ernestdefoe/garrison` | **MIT** | public Packagist | the free tier |
| `ernestdefoe/garrison-pro` | proprietary | Client Area private Composer | the paid tier |

## 1. Why two packages and not a licence key

The alternative was one MIT package with a phone-home licence check, on the model
of the 26 paid IPS apps. It was rejected, and the reason is worth keeping:

**MIT cannot be walked back.** A licence check inside MIT-licensed code is
legally removable by anybody who receives it, so the check would have been
decoration — and the standing rule from the IPS rollout is that *a check that can
never return basic is decoration, not protection*. Shipping one anyway would have
meant writing, testing and maintaining a phone-home, a grace period and a tier
resolver, all of which a user is entitled to delete.

Two packages need none of that. There is **no licence key, no phone-home, no
grace period and no expiry anywhere in Garrison.** Entitlement is possession: you
have `garrison-pro` installed or you do not, and the Client Area already decides
who may `composer require` it. That is the same model as Bespoke and AI Helper,
running on machinery that already works.

🚨 **Do not add a licence key to either package later.** If it is worth gating it
belongs in `garrison-pro`; if it is in `garrison-pro` it needs no gate.

## 2. Be honest about what this protects

Open core is a **commercial** boundary, not a technical one.

The MIT half is modifiable by anyone who receives it, which means a sufficiently
determined person can patch it to queue a verb the free tier does not offer. This
is true of every open-core product and it is the accepted trade. What makes it
work is that the paid package is where the **features actually live** — the
scheduling engine, the offsite upload, the restore safety copy, the settings
parser, the identity flow. Patching a constant does not conjure those; they have
to be rewritten. Anybody willing to do that was never a customer.

So: no obfuscation, no disguised checks, nothing adversarial in the MIT half. The
free tier should be a genuinely good product that a lot of forums happily never
pay for. That is what a funnel is.

## 3. What is in each package

Mapped onto `Dispatcher::QUEUEABLE`, which is the real boundary:

| Verb | Free (MIT) | Pro |
|---|---|---|
| `server.status` | ✅ | |
| `server.start` / `stop` / `restart` | ✅ | |
| `server.stats` | ✅ | |
| `console.tail` / `console.send` | ✅ | |
| `backup.create` / `list` / `restore` / `delete` | | ✅ |
| `config.list` / `get` / `set` | | ✅ |
| `player.verify` | | ✅ |
| `provision.templates` / `provision.install` | | ✅ |

**Free also keeps, deliberately:** the health probes, incident detection and
notifications. They ride on `server.status`, and a free tier that cannot tell you
your server has stopped accepting players is an advert, not a product. That
failure is the reason Garrison exists — paywalling it would make the paid tier
read as a hostage situation rather than an upgrade.

**Caps:** one `GarrisonAgent`, one `Server`. Enforced in the MIT package; lifted
by the presence of `garrison-pro`.

## 3a. What did NOT move, and why

Three things the earlier draft of this document said would move, and did not:

**The migrations, and the tables they create.** All thirteen stay in garrison,
including the ones only pro uses. The draft called this "the hard part" and it
was right: pro's migrations would have had to ADOPT tables that already exist
with data on any forum that had run 1.0.x, and getting that wrong loses somebody
a schedule set. Against that, the cost of the free package creating two tables it
never writes to is nothing at all. 🚨 It also keeps `Server`'s relations coherent
in one package rather than split across two, one of which can be disabled.

**The models.** Same reasoning. `Schedule`, `Identity` and `PlaySession` are
garrison's; pro uses them through the composer dependency. The row-shaping for a
schedule sits on the model itself (`toAdminArray`) precisely because both
packages render it and two copies would drift.

**The strings and the stylesheet.** Both stay in garrison. Splitting them along
the same line as the code means finding and moving every key and every rule
exactly once, and the failure mode for missing one is loud and user-facing: a raw
`ernestdefoe-garrison.forum.backups.title` in a paying customer's panel. It would
also leave a forum that removed pro with half-styled remnants.

So the line that was actually built is: **garrison owns the schema, the models,
the strings and the styles; pro owns the behaviour and the components.**

## 4. The split — what actually moved

`garrison-pro` is a Flarum extension in its own right, with `ernestdefoe/garrison`
as a hard composer dependency. What physically left the MIT repo:

| Moved to pro | |
|---|---|
| `Api/Controller/IdentityController` | the in-game identity flow |
| `Api/Controller/PlaytimeController` | playtime and the leaderboard |
| `Players/Linker` | issues and checks the in-game code |
| `Schedule/Runner` | the scheduling engine |
| `Console/ScheduleCommand`, `Console/BackupCommand` | and both their cron entries |
| the schedule and identity write-actions out of `AdminController` | into pro's own controller |
| `js/.../Backups`, `Settings`, `Leaderboard`, `LinkIdentity`, `ProfilePlaytime` | |
| `js/.../Provision`, `Schedules`, and the admin identities section | |

Everything else stayed. See §3a for the three things that deliberately did not
move, and why.

### How pro attaches, in both languages

**PHP:** one call, `Edition::enablePro()`, from pro's service provider. 🚨 From a
service provider rather than a `class_exists()` sniff inside garrison, because
Flarum does not boot the providers of DISABLED extensions — an operator who
installs pro and switches it off must get the free tier, and a sniff would light
every paid feature back up silently.

**JS:** pro PUSHES onto a queue; garrison drains it. `GarrisonPanelQueue` for the
server page, `GarrisonAdminQueue` for the admin panel (three slots: page, host,
server). 🚨 A queue rather than a function call because the two bundles are
separate files and nothing orders them — pro's may evaluate first. Same pattern
garrison's widget hosts already use.

Garrison also publishes a small runtime surface, `globalThis.Garrison`
(`command`, `awaitCommand`, `refresh`, `subscribe`, `byId`, `bytes`, `duration`).
🚨 This exists so pro does not carry its own copy of the store: the store is an
INSTANCE — one poll loop, one cache — and a second copy would double the request
rate on exactly the forums that paid. Pure helpers may be copied; instances must
be shared. It is a published interface: the two packages can be on different
versions on a real forum, so add to it rather than changing it.

### The agent stays MIT and stays whole

One binary, implementing every verb, in the MIT repo. It is a dumb executor: it
does what the forum it is paired with asks. Splitting it would mean shipping two
binaries and a capability negotiation, for a boundary §2 already concedes is
commercial rather than technical.

## 5. Where the caps are enforced

Three chokepoints, all server-side, all in the **MIT** package. The UI may
*reflect* entitlement; it may never be the thing that enforces it.

### 5.1 Commands — `src/Agent/Dispatcher.php`

`Dispatcher::queue()` is the single path to an agent, by construction:

> Written as one method, called from one place, so there is no surface that can
> queue a command without passing through it.

`QUEUEABLE` ships containing only the free verbs. `garrison-pro` extends it —
which makes "what may be queued" a published, testable list rather than a
scattering of conditionals, and means the MIT package has no dead branches for
features it does not contain.

The verb gate and the cap both went into `queue()` itself, ABOVE the call to
`assertPermitted()` — not inside it, as the draft proposed.

🚨 That is a stronger arrangement than getting the order right inside
`assertPermitted()`, and it is the whole reason it was done this way: a check
that lives above the permission method **cannot** be ordered below the
`garrison.manage` shortcut, because it is not in that method at all. An
administrator on the free tier still holds `garrison.manage`, so a gate placed
below that shortcut would never run for the one person most likely to own a
second server — the exact shape of the bug `DispatcherOrderTest` exists to catch.
Structure beats vigilance: this one cannot drift back.

Verified against a real administrator on dev, with `manage=true`: lifecycle on
the entitled server queued, `backup.create` and `config.set` refused, and
lifecycle on a server beyond the cap refused.

Refusals are a `ValidationException` with a distinct code (`not_entitled`), never
a 404 and never a generic failure: an operator must be able to tell "this tier
does not include that" from "this is broken".

### 5.2 Servers coming into existence — `src/Agent/Gateway.php`

Servers are **not created by an admin action**. `Gateway::recordStatus()` creates
a row the first time an agent reports a server it is running, so the second server
on a free forum arrives without anybody clicking anything and the cap cannot live
in a form.

Rule: **keep recording, gate the use.**

- Extra servers are still created and their status still recorded. The row is
  cheap, and installing `garrison-pro` later then restores a continuous history
  rather than a gap starting the moment somebody paid.
- Extra servers are **invisible on the forum** — no page, no widget row, no
  notifications. A free forum shows one server publicly.
- They **are** visible in the admin panel, plainly labelled, with the upgrade
  path. Hiding them entirely would read as Garrison having lost a server.

### 5.3 Hosts — `AdminController::pair()` and `src/Console/PairCommand.php`

Two creation paths, both must check. Pairing a second host is refused **at pairing
time** with a message naming the reason — this is the one cap enforceable before
anything exists, so it should be.

## 6. Which host, which server?

The cap needs a deterministic answer to *which one*, and the wrong answer locks
somebody out of the server they care about.

- **Default:** the oldest, by id. Stable, and it does not move when a host goes
  offline.
- **The operator may choose**, in the admin panel, at any time. This matters most
  on removal: somebody who uninstalls `garrison-pro` with three servers must pick
  which one stays live rather than have Garrison pick for them.
- Stored as `entitled_agent_id` / `entitled_server_id`, surviving install and
  uninstall, so removing and reinstalling pro is idempotent.

## 7. Removing pro is never destructive

The house rule — *miserable-but-legal, never destructive* — sharpened here,
because Garrison is holding somebody's game server.

When `garrison-pro` is uninstalled or disabled:

- **Nothing is deleted.** Not a backup, not a schedule, not an identity link, not
  a metric sample.
- **Running servers keep running.** Garrison never stops a server it can no longer
  manage.
- **Scheduled work pauses; it does not fire and it does not vanish.** A nightly
  restart that silently stops happening is exactly the invisible failure this
  product exists to catch.
- **Backups already taken remain listed and remain on disk.** Offsite copies stop
  being made; the ones already made are untouched.
- **Existing identity links stay linked** and recorded playtime is kept.
- **The agent is not unpaired and its token is not revoked.**

The test of each: *if they installed pro again tomorrow, would anything be
missing?* The answer must be no. 🚨 This is a stronger requirement than Flarum's
default — disabling an extension usually just stops its code running, but here the
tables belong to pro while the data belongs to the customer.

## 8. Test discipline

The standing rule from the IPS rollout still applies, in its two-package form:

1. **MIT package alone:** every paid verb refused, both caps applied, and the free
   feature set fully working. A free tier that is quietly broken is worse than no
   free tier.
2. **With pro installed:** no paid verb refused, no cap applied.
3. **Pro removed again:** §7 holds, asserted against real rows, not mocks.

Per the wiring rule, assert against the **dispatcher and the gateway**, not against
an entitlement helper in isolation. A passing test on a gate nothing routes through
is the failure mode here — the one `DispatcherOrderTest` already exists to catch.

## 9. Things not to do

- **Do not enforce by hiding buttons.** A disabled control whose API would honour
  the request is the decorative-control trap, and here it is also the revenue
  boundary.
- **Do not make the free tier expire.** It is a tier, not a trial.
- **Do not paywall the alerting.** See §3.
- **Do not let anything about entitlement stop a game server.** See §7.
- **Do not hardcode English into any refusal.** Every message is a translation key.
- **Do not ship a licence key.** See §1.
