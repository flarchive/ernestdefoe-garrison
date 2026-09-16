# The agent

One static Go binary. It runs on your game host, **dials out** to the forum, and
is the only thing in this product that touches a file, a container or a process.

Everything here is configured on the HOST, in a file only you can write. That is
the security boundary, not a convenience: the forum is a PHP application on the
public internet running third-party extension code, and it is the part of this
system most likely to be compromised. It can ask for a backup of a server it
already knows about. It cannot say what gets archived, where it is written, or
where a copy is sent.

```bash
# on your machine, for a Linux game host
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o garrison-agent ./cmd/garrison-agent

# on the game host
garrison-agent --config /etc/garrison/agent.json --check
```

---

## Configuring a server on the host

Everything that names a **path, a file or a credential** is configured here, on
the game host, and never in the forum. That is the security boundary, not a
convenience: the forum is a PHP application on the public internet running
third-party extension code, and it is the part of this system most likely to be
compromised. It can ask for a backup of a server it already knows about. It
cannot say what gets archived, where it is written, or where a copy is sent.

```json
{
  "id": "valheim",
  "name": "Shattered Pact",
  "driver": "docker",
  "container": "valheim",
  "game": "valheim",
  "stopGraceSeconds": 120,

  "backupRoot": "/srv/valheim",
  "backupPaths": ["worlds", "server.cfg"],
  "backupDir": "/srv/valheim/garrison-backups",
  "backupKeep": 14,

  "offsite": {
    "endpoint": "https://s3.us-west-002.backblazeb2.com",
    "region": "us-west-002",
    "bucket": "shattered-pact-backups",
    "prefix": "valheim",
    "accessKey": "…",
    "secretKey": "…",
    "pathStyle": true,
    "keep": 30
  }
}
```

### Editable settings

🚨 **There is no file manager, and that absence is the feature.** The operator
declares which files may be read and changed; the forum names one by its `id`
and a path never crosses the wire. `keys`, when given, narrows it further — an
operator can let a moderator change the message of the day without that
moderator being three lines away from the RCON password in the same file.

```json
"config": [
  {
    "id": "props",
    "label": "server.properties",
    "path": "/srv/minecraft/server.properties",
    "format": "properties",
    "keys": ["motd", "max-players", "view-distance"]
  },
  {
    "id": "startup",
    "label": "Startup arguments",
    "path": "/srv/minecraft/start.env",
    "format": "properties",
    "readOnly": true
  }
]
```

Formats are `properties` (`key=value`, `#` comments — Minecraft and most Java
servers) and `ini` (the same with `[sections]`). Keys not in `keys` are still
shown, greyed: a setting somebody cannot find is one they go and edit by hand.
Comments in the file become the help text under each field, because the game
already wrote down what its settings do.

Editing rewrites one line in place. Comments, blank lines, ordering and
indentation all survive, and the write goes through a temporary file and a
rename so an interruption cannot leave a half-written config that resets the
server to defaults on next start.

### Off-site copies

Any S3-compatible provider: AWS S3, Backblaze B2, Cloudflare R2, Wasabi,
MinIO. A copy is made after each successful backup, and retention runs against
the bucket separately from the local one — `keep` off-site is usually larger
than `backupKeep`, because the whole point of the remote copy is that it
outlives the host.

- **`endpoint` must be `https://`.** Uploads are signed with
  `UNSIGNED-PAYLOAD`, which avoids reading a multi-gigabyte archive twice; that
  trade is only safe under TLS, so a plain `http://` endpoint is refused with an
  error saying why rather than silently accepted.
- **`pathStyle` is what most non-AWS providers need.** AWS serves a bucket as
  `<bucket>.s3.amazonaws.com`; MinIO and, depending on setup, B2 and R2 serve it
  as `<endpoint>/<bucket>`. Getting it wrong produces a DNS failure or a 404,
  which reads as a wrong endpoint and sends you looking in the wrong place.
- **`region`** is required. Providers that do not use regions accept `auto` or
  `us-east-1`.
- Archives over 64 MiB are uploaded in parts, because S3 caps a single PUT at
  5 GiB — without that, off-site backups work for a year and then stop the day
  the world gets big.

A failed upload never fails the backup. A local archive that exists is worth
more than a copy that did not arrive: the common disasters are all recovered
from the local one. The forum's panel says whether copies are landing and shows
the provider's own error when they are not.

## What is deliberately not here

- **No credentials in the forum.** Off-site keys live in this file. Putting
  them in the admin panel would mean storing them in the most attackable part
  of the system and then sending them over the wire to get here.
- **No systemd or Windows driver.** The interface has room for both; adding
  them before the interface was proved would have been guessing.
- **No `exec` verb, and there will not be one.** A fully compromised forum can
  restart a server it already knows about. It cannot ask for a shell, because
  there is no verb through which it could.

## Running the agent under systemd

🚨 **`KillMode=process`, or restarting the agent kills every game on the host.**

systemd's default is `control-group`: stopping a unit kills everything in its
cgroup, and the game servers the agent started are in it. The agent deliberately
detaches from its children rather than signalling them — stopping the agent must
never stop the games, or nobody would let it auto-update — and systemd's default
defeats that from the outside. Found on the dev host, where every restart of the
agent silently took the game down with it.

```ini
[Unit]
Description=Garrison agent
After=network-online.target

[Service]
ExecStart=/usr/local/bin/garrison-agent --config /etc/garrison/agent.json
Restart=always
RestartSec=5

# 🚨 Not the default. See above: without this, `systemctl restart garrison-agent`
# stops every game server on this machine.
KillMode=process

[Install]
WantedBy=multi-user.target
```

## Linking forum accounts to players

A player proves who they are **inside the game**, not on the forum. A form that
asks for an in-game name and believes the answer lets anybody claim the
community's best-known player and inherit their playtime and rank.

```json
"players": {
  "preset": "minecraft",
  "verifyMessage": "Garrison code: {code}"
}
```

Garrison whispers a six-character code to that player using the preset's `say`
template; they read it in the game and type it back on the forum. Presets that
have no whisper command cannot verify, and the forum does not offer the flow
there rather than showing a button that always fails.

🚨 **The forum never composes the console line.** It sends a name and a code; the
agent renders the operator's template and refuses any player it cannot currently
see in the game. That matters because verification is something ordinary members
do — and a game console is where `ban`, `op` and `give` live. A player called
`alice /op mallory` would otherwise turn one command into two.

## Installing a server from the forum

🚨 **The forum sends a template name and a server id. That is all it sends.**

This is the one feature where the agent gains a server it did not have at
startup, which means it writes its own config — exactly the surface where "the
operator decides what may run" could quietly become "the forum decides what may
run". It does not: the install directory, the start command, the driver and the
Steam app id all come from a template in this file. A fully compromised forum
can install one of the games its operator already listed, into the directory its
operator already chose, and nothing else anywhere else.

```json
"templates": [
  {
    "id": "valheim",
    "label": "Valheim dedicated server",
    "driver": "process",
    "game": "valheim",
    "steamApp": 896660,
    "installRoot": "/srv/garrison",
    "command": "./start_server.sh",
    "stopGraceSeconds": 120,
    "backupPaths": ["worlds"],
    "backupKeep": 14,
    "players": { "preset": "valheim" }
  }
]
```

An agent with no `templates` cannot be asked to install anything, and that is
the default. The id a new server is given must match `[a-z0-9][a-z0-9_-]{0,31}`
— it cannot express `..`, a slash or a leading dash, so there is no clever
composition to reason about. Everything else a provisioned server inherits
(backup paths, player tracking, editable config files) comes from the template,
so an operator sets it once per game rather than once per server.

`steamApp` needs `steamcmd` on the host. A template without one installs nothing
and simply prepares the directory, for games that are not on Steam.

The picker in the admin panel shows the label and the game and **never the
install path or the command** — knowing where a game lives on disk is the first
half of doing something about it.

## Notes for whoever picks this up

- The agent **dials out**. The forum never connects to the game host, so a
  host needs no inbound port, no port forward and no static IP. That is what
  lets somebody run the Minecraft server on the box under their desk.
- Stopping the agent must never stop the games. `garrison-agent` detaches from
  its children rather than signalling them, or nobody would let it auto-update.
- The first `server.stats` for any server reports `cpuPercent: 0` and this is
  correct: CPU is a rate, and one reading of a counter is not one. The forum
  should sample twice before drawing anything.
