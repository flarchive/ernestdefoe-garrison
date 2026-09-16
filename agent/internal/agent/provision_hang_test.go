package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ernestdefoe/garrison/internal/driver"
	"github.com/ernestdefoe/garrison/internal/protocol"
	"github.com/ernestdefoe/garrison/internal/provision"
)

/*
🚨 provision.install must ANSWER, whatever happens to the install.

Found on a real host: installing Minecraft before its JRE was present left the
command at `delivered` for ever. Nothing failed visibly — the forum simply never
heard anything back, which reads as an install still in progress and waits for
something that will never finish.

The install itself runs in a goroutine on purpose (a download can take hours, and
the poll that started it is answered in seconds). That is exactly what makes this
worth a test: the handler's duty is to accept and return promptly, and no outcome
of the background work may change that.
*/
func TestProvisionInstallAlwaysAnswers(t *testing.T) {
	cases := []struct {
		name string
		tpl  provision.Template
	}{
		{
			name: "a missing prerequisite",
			tpl: provision.Template{
				ID: "needsthing", Label: "Needs a thing", Driver: "fake",
				Command: "./start.sh", NeedsBinary: "definitely-not-here-12345",
			},
		},
		{
			name: "a download that cannot resolve",
			tpl: provision.Template{
				ID: "baddownload", Label: "Bad download", Driver: "fake",
				Command: "./start.sh", Download: "https://127.0.0.1:1/nothing.tar.gz", Archive: "tar.gz",
			},
		},
		{
			name: "nothing to install at all",
			tpl: provision.Template{
				ID: "plain", Label: "Plain", Driver: "fake", Command: "./start.sh",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tpl := c.tpl
			tpl.InstallRoot = t.TempDir()

			a, err := New(context.Background(),
				[]driver.Server{{ID: "existing", Name: "Existing", Driver: "fake"}},
				driver.Set{"fake": &fakeDriver{name: "fake"}})
			if err != nil {
				t.Fatal(err)
			}

			a.Provisioning([]provision.Template{tpl}, func(driver.Server) error { return nil })

			params, _ := json.Marshal(protocol.ProvisionParams{Template: tpl.ID, ID: "newone"})

			answered := make(chan protocol.Response, 1)

			go func() {
				answered <- a.Handle(context.Background(), protocol.Request{
					ID: "1", Verb: protocol.VerbProvisionInstall, Params: params,
				}, func(protocol.Event) {})
			}()

			select {
			case <-answered:
				// Accepted or refused: either is an answer, which is the point.
			case <-time.After(15 * time.Second):
				t.Fatal("provision.install never answered — the forum would wait for ever")
			}
		})
	}
}
