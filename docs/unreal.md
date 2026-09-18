# Unreal Engine servers

Garrison runs an Unreal dedicated server like any other — `process` or `docker`
driver, health checks, console, backups. Player tracking is the one part that
needs twenty lines of C++ in your game, and this explains why.

## Why this one is a contract and not a preset

Every other preset in Garrison matches lines the server already prints. Unreal
cannot work that way.

The engine **does** print a named join, in `World.cpp`:

```
LogNet: Join succeeded: alice
```

It prints **no named leave**. A player disconnecting produces connection-close
lines that carry a `UNetConnection`, not a player name. Garrison needs both
halves — it refuses a configuration with only one, deliberately, because a
watcher that sees joins and never leaves shows a population that only ever
grows. Yesterday's players stay listed for ever, playtime is nonsense, and the
"who is online" widget lies in a way nobody notices for weeks.

So the `unreal` preset matches two lines your game prints on purpose.

## What to add

One log category that exists for no other reason, and two calls.

```cpp
// GarrisonLog.h
DECLARE_LOG_CATEGORY_EXTERN(LogGarrison, Log, All);

// GarrisonLog.cpp
DEFINE_LOG_CATEGORY(LogGarrison);
```

```cpp
// YourGameMode.cpp
void AYourGameMode::PostLogin(APlayerController* NewPlayer)
{
    Super::PostLogin(NewPlayer);

    if (NewPlayer && NewPlayer->PlayerState)
    {
        UE_LOG(LogGarrison, Log, TEXT("player joined: %s"),
            *SafeName(NewPlayer->PlayerState->GetPlayerName()));
    }
}

void AYourGameMode::Logout(AController* Exiting)
{
    // 🚨 BEFORE Super, which tears the player state down. Afterwards there is
    // no name left to print and the player never leaves as far as Garrison is
    // concerned.
    if (Exiting && Exiting->PlayerState)
    {
        UE_LOG(LogGarrison, Log, TEXT("player left: %s"),
            *SafeName(Exiting->PlayerState->GetPlayerName()));
    }

    Super::Logout(Exiting);
}
```

Then in the agent config:

```json
{
  "id": "zone-ashenreach",
  "game": "unreal",
  "players": { "preset": "unreal" }
}
```

## `SafeName`, and why you need it

Keep only `A-Za-z0-9_-`, cap it at 32 characters, and skip the announcement
entirely if nothing survives.

🚨 **This is a security control, not tidiness.** The preset is anchored from the
start of the line through the log prefix, because chat travels in the same log
and a player typing

```
LogGarrison: player joined: bob
```

would otherwise forge a player — or, with the leave wording and a real player's
name, evict one. Anchoring defeats that, because a chat line always carries its
own log category between the prefix and anything a player typed.

The name charset is the other half. A display name containing a newline can
write a *second* line into the log, starting at column zero, where the anchor no
longer protects you. An allow-list is used rather than stripping "the dangerous
ones", because a deny-list means enumerating every character that could ever
matter and the first one missed is the one somebody uses.

Skipping the announcement when nothing survives matters too: a placeholder like
`unknown` makes Garrison track a player called unknown, and a second such player
collides with the first and un-announces them on leave.

## What you do not get

- **No `say`.** Unreal has no console command every game agrees on, so in-game
  verification (the code Garrison whispers to prove somebody controls an
  account) needs a `say` configured per server, pointing at whatever your game
  calls it.
- **Timestamps optional.** A server started with `-NoLogTimes` prints no
  bracketed prefix. The preset handles both.

## Health

Unreal gives you nothing standard to health-check, so use a line your own
startup prints once the server is genuinely serving:

```json
"health": [
  { "name": "the zone is accepting players",
    "type": "log_match", "pattern": "Zone ready on ", "within": "5m", "threshold": 1 }
]
```

🚨 Match something that means *ready*, not something that means *started*. A
pattern matching an early boot line reports healthy on a server that is still
loading and will refuse every connection it gets.
