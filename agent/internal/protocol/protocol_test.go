package protocol

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ernestdefoe/garrison/internal/backup"
)

// 🚨 protocol.Backup is a deliberate duplicate of backup.Backup: protocol is
// the wire contract and must not depend on an implementation package, because
// the other half of this contract is PHP. Duplication is the right call and it
// is also how the two drift — a field added to one and not the other ships an
// archive the forum cannot see, or a column the agent never fills.
//
// Compared by their JSON shape rather than by reflect.DeepEqual on the types,
// because the JSON is what actually crosses the wire: a renamed tag breaks the
// forum just as thoroughly as a renamed field, and only this catches it.
func TestProtocolBackupMatchesImplementation(t *testing.T) {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

	fromImpl, err := json.Marshal(backup.Backup{ID: "srv-20260915-120000-a1b2.tar.gz", Size: 42, At: at, Safety: true})
	if err != nil {
		t.Fatal(err)
	}

	fromWire, err := json.Marshal(Backup{ID: "srv-20260915-120000-a1b2.tar.gz", Size: 42, At: at, Safety: true})
	if err != nil {
		t.Fatal(err)
	}

	if string(fromImpl) != string(fromWire) {
		t.Fatalf("the two Backup types no longer marshal alike:\n  backup:   %s\n  protocol: %s", fromImpl, fromWire)
	}
}
