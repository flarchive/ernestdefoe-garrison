// Package protocol defines the wire contract between the forum and an agent.
//
// 🚨 The verb set in this file is the security boundary of the whole product.
//
// The forum is a PHP application, on the public internet, running third-party
// extension code. It is the thing most likely to be compromised. So the agent
// does not accept commands — it accepts VERBS, from the closed set below, and
// anything else is refused before it reaches a driver. A fully compromised
// forum can ask to restart a server it already knows about. It cannot ask for
// a shell, and there is deliberately no verb through which it could.
//
// That is why there is no "exec", no "run", no "script" and no field anywhere
// in Request that is passed to a shell. If a future feature seems to need one,
// it needs a new named verb with its own validation instead.
package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// Verb is one operation the agent will perform. The set is closed: see Known.
type Verb string

const (
	// Agent-level.
	VerbPing       Verb = "agent.ping"
	VerbAgentInfo  Verb = "agent.info"
	VerbServerList Verb = "server.list"

	// Lifecycle.
	VerbStatus  Verb = "server.status"
	VerbStart   Verb = "server.start"
	VerbStop    Verb = "server.stop"
	VerbRestart Verb = "server.restart"

	// Telemetry.
	VerbStats Verb = "server.stats"

	// Console.
	VerbConsoleTail Verb = "console.tail"
	VerbConsoleSend Verb = "console.send"

	/*
	 * Backups.
	 *
	 * 🚨 Adding verbs widens the security boundary, so these are shaped to
	 * carry as little authority as possible. backup.restore takes an ID from
	 * backup.list and nothing else — no path, no destination, no "and also
	 * stop the server". Everything those would enable is either already a
	 * verb of its own, or deliberately not available at all.
	 */
	VerbBackupCreate  Verb = "backup.create"
	VerbBackupList    Verb = "backup.list"
	VerbBackupRestore Verb = "backup.restore"
	VerbBackupDelete  Verb = "backup.delete"

	/*
	 * Configuration.
	 *
	 * 🚨 There is no config.read-any-file and no config.write-any-file, and
	 * that absence is the feature. These operate on an ID from a list the
	 * OPERATOR declared on the host; a path never crosses this wire in either
	 * direction. A file manager here would be arbitrary write access on a
	 * machine that runs a start script, reachable from a PHP forum on the
	 * public internet — which is arbitrary code execution with extra steps.
	 */
	VerbConfigList Verb = "config.list"
	VerbConfigGet  Verb = "config.get"
	VerbConfigSet  Verb = "config.set"

	/*
	 * Identity.
	 *
	 * 🚨 The forum sends a NAME and a CODE. It does not send a console line,
	 * and it cannot: the agent renders the whisper from the operator's own
	 * template, and only for a player it currently sees in the game. A verb
	 * that let the forum compose console text would hand every forum member
	 * the ban, op and give commands, since verification is a thing ordinary
	 * members do.
	 */
	VerbPlayerVerify Verb = "player.verify"

	/*
	 * Provisioning.
	 *
	 * 🚨 The forum sends a TEMPLATE NAME and a SERVER ID, and nothing else.
	 * The install directory, the start command, the driver and the Steam app
	 * id all come from a template in the agent's own config file. There is no
	 * field in ProvisionParams that can name a path, a command or an app —
	 * which matters more here than anywhere else in this protocol, because
	 * this is the one verb that makes the agent write its own allowlist.
	 */
	VerbProvisionTemplates Verb = "provision.templates"
	VerbProvisionInstall   Verb = "provision.install"
)

// known is the entire set of verbs this agent will ever dispatch.
//
// 🚨 A map literal, not a switch in the dispatcher and not a naming
// convention. It can be enumerated, which means it can be tested and it can be
// shown to an operator; "everything starting with server." cannot be either.
var known = map[Verb]struct{}{
	VerbPing:          {},
	VerbAgentInfo:     {},
	VerbServerList:    {},
	VerbStatus:        {},
	VerbStart:         {},
	VerbStop:          {},
	VerbRestart:       {},
	VerbStats:         {},
	VerbConsoleTail:   {},
	VerbConsoleSend:   {},
	VerbBackupCreate:  {},
	VerbBackupList:    {},
	VerbBackupRestore: {},
	VerbBackupDelete:  {},
	VerbConfigList:    {},
	VerbConfigGet:     {},
	VerbConfigSet:     {},
	VerbPlayerVerify:  {},

	VerbProvisionTemplates: {},
	VerbProvisionInstall:   {},
}

// Known reports whether v is a verb this agent implements. Everything else is
// refused with CodeUnknownVerb without a driver ever being consulted.
func Known(v Verb) bool {
	_, ok := known[v]
	return ok
}

// Verbs returns every known verb, for agent.info and for tests that assert the
// set has not grown by accident.
func Verbs() []Verb {
	out := make([]Verb, 0, len(known))
	for v := range known {
		out = append(out, v)
	}
	return out
}

// NeedsServer reports whether a verb operates on a particular server, and so
// requires Request.Server to name one the agent already knows.
func NeedsServer(v Verb) bool {
	switch v {
	case VerbPing, VerbAgentInfo, VerbServerList:
		return false
	case VerbProvisionTemplates, VerbProvisionInstall:
		// 🚨 Provisioning CREATES a server, so requiring it to name one that
		// already exists would make the verb impossible to call. The id it
		// carries is validated by internal/provision instead, which is the only
		// place that can turn it into a path.
		return false
	default:
		return true
	}
}

// Error codes. Every failure the forum can see is one of these, so the UI can
// branch on them rather than on message text.
const (
	CodeUnknownVerb    = "unknown_verb"
	CodeUnknownServer  = "unknown_server"
	CodeBadRequest     = "bad_request"
	CodeDriverFailed   = "driver_failed"
	CodeNotSupported   = "not_supported"
	CodeAlreadyRunning = "already_running"
	CodeNotRunning     = "not_running"
	CodeTimeout        = "timeout"
	CodeInternal       = "internal"
)

// Error is a machine-readable failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errf builds an Error with a formatted message.
func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Request is one instruction from the forum.
//
// 🚨 Note what is absent: no command, no path, no image, no shell. Params is
// per-verb and every verb validates its own, so adding a field cannot
// accidentally widen what the agent will do.
type Request struct {
	ID     string          `json:"id"`
	Verb   Verb            `json:"verb"`
	Server string          `json:"server,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers exactly one Request, by ID.
type Response struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Error *Error          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Event is unsolicited: console lines and stats samples that belong to a
// long-running request (Stream carries that request's ID).
type Event struct {
	Stream string          `json:"stream"`
	Kind   string          `json:"kind"`
	At     time.Time       `json:"at"`
	Data   json.RawMessage `json:"data"`
}

// Frame is what actually crosses the wire, in either direction. Exactly one
// field is set.
type Frame struct {
	Request  *Request  `json:"req,omitempty"`
	Response *Response `json:"res,omitempty"`
	Event    *Event    `json:"evt,omitempty"`
}

// ---- verb payloads -------------------------------------------------------

// StopParams is the payload of server.stop and server.restart.
type StopParams struct {
	// GraceSeconds overrides the server's configured grace period. Zero means
	// use the configured one; it never means "kill immediately".
	GraceSeconds int `json:"graceSeconds,omitempty"`
}

// TailParams is the payload of console.tail.
type TailParams struct {
	// History is how many lines of scrollback to send before following.
	History int `json:"history,omitempty"`
	// Follow keeps the stream open and sends new lines as they arrive.
	Follow bool `json:"follow,omitempty"`
}

// SendParams is the payload of console.send.
type SendParams struct {
	Line string `json:"line"`
}

// BackupParams is the payload of backup.restore and backup.delete.
//
// 🚨 An ID, and only an ID. The agent validates it against the pattern IT
// generates, so there is no shape of this field that names a path.
type BackupParams struct {
	ID string `json:"id"`
}

// ConfigParams is the payload of config.get and config.set.
//
// 🚨 `File` is an ID from the operator's declared list, never a path. `Key`
// must already exist in that file — the agent refuses to create settings,
// because a typo would otherwise add one the game ignores and the operator
// would see it saved, see no effect, and conclude the panel is broken.
type ConfigParams struct {
	File    string `json:"file"`
	Section string `json:"section,omitempty"`
	Key     string `json:"key,omitempty"`
	Value   string `json:"value,omitempty"`
}

// VerifyParams is the payload of player.verify.
//
// 🚨 Two short strings and nothing else. The agent refuses a player it cannot
// currently see in the game, and refuses either field if it carries anything a
// console would read as a separator — see players.VerifyLine, which is where
// the security of this feature actually lives.
type VerifyParams struct {
	Player string `json:"player"`
	Code   string `json:"code"`
}

// ProvisionParams is the payload of provision.install.
//
// 🚨 Two strings, both validated before either can become a path: Template
// against the operator's declared list, ID against a pattern that cannot
// express `..`, a slash or a leading dash. See internal/provision.
type ProvisionParams struct {
	Template string `json:"template"`
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
}

// ---- results -------------------------------------------------------------

// State is the lifecycle state of a server, normalised across drivers.
//
// 🚨 Normalised deliberately. Docker says "exited", systemd says "inactive",
// a bare process says nothing at all. If those differences reach the forum,
// every consumer has to know about every driver.
type State string

const (
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateStopping State = "stopping"
	StateCrashed  State = "crashed"
	StateUnknown  State = "unknown"
)

// Status is what server.status returns.
type Status struct {
	Server string `json:"server"`

	/*
	 * Name is what the operator called this server in the agent's config.
	 *
	 * 🚨 Shipped because otherwise it is a setting that does nothing. An
	 * operator writes `"name": "Shattered Pact"`, the forum never receives it,
	 * and the panel shows the id — which reads as Garrison ignoring them.
	 * Found on the first production install, where a server carefully named in
	 * the config appeared as "valheim".
	 *
	 * The forum uses it ONLY when it first sees a server. After that an admin
	 * owns the name, and an agent that re-asserted it every poll would undo a
	 * rename fifteen seconds after somebody made it.
	 */
	Name string `json:"name,omitempty"`

	Driver string `json:"driver"`
	Game   string `json:"game,omitempty"`
	State  State  `json:"state"`
	PID    int    `json:"pid,omitempty"`

	// 🚨 A POINTER, so a server that has never started sends nothing.
	//
	// `omitempty` does not work on a time.Time: it is a struct, so the zero
	// value is still marshalled — as "0001-01-01T00:00:00Z". PHP parsed that
	// happily and the status page showed "Started Dec 31, 0000" for a process
	// that had never run. omitempty on a pointer does what it looks like.
	Since   *time.Time `json:"since,omitempty"`
	Detail  string     `json:"detail,omitempty"`
	Healthy *bool      `json:"healthy,omitempty"`

	// Stats rides along with a status report rather than needing its own
	// round trip, because the page that shows one always shows the other.
	Stats *Stats `json:"stats,omitempty"`

	// Health is the readiness verdict. Deliberately separate from State:
	// "running" and "players can get in" are different facts, and the whole
	// product exists because they were conflated for twenty hours.
	Health any `json:"health,omitempty"`

	/*
	 * Backups ride along with status, for the same reason Stats does — and
	 * for one more.
	 *
	 * 🚨 A LIST SOMEBODY HAS TO ASK FOR IS A LIST THEY SEE TOO LATE.
	 *
	 * The obvious design is a backup.list verb the forum queues when somebody
	 * opens the backups panel. It works, and it puts a round trip through a
	 * long-poll — up to half a minute of an empty panel — between an operator
	 * and the answer to "is there anything to restore?". That question is
	 * asked at exactly one moment: just after something went badly wrong. The
	 * worst possible time to show a spinner is the moment somebody is deciding
	 * whether they have lost a world.
	 *
	 * Shipping it on the poll costs a directory read per server per cycle and
	 * a few hundred bytes, and means the forum can always answer instantly,
	 * even while the host is mid-restart or has just gone offline. backup.list
	 * still exists as a verb, because a forum that has just paired an agent
	 * should not have to wait a poll for its first answer either.
	 */
	Backups []Backup `json:"backups,omitempty"`

	/*
	 * Players is who is in the game right now, read from its log.
	 *
	 * 🚨 A SET, not a stream of events, and nil when the operator has not
	 * configured how to read them — which is distinct from an empty slice
	 * meaning "configured, and nobody is playing".
	 *
	 * Shipping the whole set every poll is what makes this self-correcting. An
	 * events design ("alice joined", "alice left") never recovers from a
	 * dropped poll, an agent restart or a log rotation: the forum would show
	 * somebody in a game they left last Tuesday with no way to notice. Diffing
	 * an authoritative set costs a few bytes and cannot drift.
	 */
	Players []string `json:"players,omitempty"`

	// PlayersKnown distinguishes "nobody is playing" from "this server does not
	// report players", which look identical in an empty list and mean opposite
	// things on a panel.
	PlayersKnown bool `json:"playersKnown,omitempty"`

	// Offsite is what the agent knows about copies to remote storage. Nil when
	// the operator has not configured any.
	Offsite *OffsiteStatus `json:"offsite,omitempty"`
}

// OffsiteStatus is the agent's account of remote copies.
//
// 🚨 Reported from what the agent REMEMBERS, not by asking the bucket.
//
// Listing the bucket on every poll would be an S3 API call per server every
// twenty-five seconds — a real bill, a real rate limit, and a status report
// that fails whenever a provider has a bad minute. What an operator actually
// needs to know is "is this on, and did the last copy work", and the agent
// already knows both from the copy it made.
//
// The honest cost is that this resets when the agent restarts, so a fresh agent
// reports Configured with no LastAt. The forum says exactly that rather than
// implying nothing has ever been copied.
type OffsiteStatus struct {
	Configured bool `json:"configured"`

	// Bucket and endpoint, so the panel can say WHERE copies go. Never the
	// keys: those are on the host and stay there.
	Bucket string `json:"bucket,omitempty"`

	LastAt    *time.Time `json:"lastAt,omitempty"`
	LastOK    bool       `json:"lastOk,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// Backup is one archive the agent is holding.
//
// 🚨 Duplicated from internal/backup rather than imported, because protocol is
// the wire contract and must not depend on an implementation package — the
// forum's half of this contract is PHP and has no idea that package exists.
// The two are kept in step by TestProtocolBackupMatchesImplementation.
type Backup struct {
	ID     string    `json:"id"`
	Size   int64     `json:"size"`
	At     time.Time `json:"at"`
	Safety bool      `json:"safety,omitempty"`
}

// Stats is one resource sample.
//
// 🚨 The same struct whatever the driver. A container reads it from the
// Docker API, a bare process from cgroup v2 or by walking /proc — see
// internal/driver. Nothing above the driver interface may care which.
type Stats struct {
	Server      string    `json:"server"`
	At          time.Time `json:"at"`
	CPUPercent  float64   `json:"cpuPercent"`
	MemoryBytes uint64    `json:"memoryBytes"`
	MemoryLimit uint64    `json:"memoryLimit,omitempty"`
	Processes   int       `json:"processes,omitempty"`
	Source      string    `json:"source"` // "docker" | "cgroup2" | "proc" | "ps"
}

// Line is one line of console output.
type Line struct {
	Server string    `json:"server"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
	Stderr bool      `json:"stderr,omitempty"`
}

// AgentInfo is what agent.info returns: enough for the forum to decide what to
// offer without guessing.
type AgentInfo struct {
	Version string   `json:"version"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Drivers []string `json:"drivers"`
	Verbs   []string `json:"verbs"`
	Servers int      `json:"servers"`
}
