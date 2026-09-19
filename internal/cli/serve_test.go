package cli_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ushineko/terrariabonker/internal/cli"
	"github.com/ushineko/terrariabonker/internal/patch"
	"github.com/ushineko/terrariabonker/internal/service"
)

/*
The long-lived worker the window talks to.

It exists because locating the player is nearly all of a read's cost, so
repeating it per request makes a live sync unaffordable. What is checked here is
the protocol and the refusals -- the operations behind it are the same ones the
command line runs, and are covered where they live.
*/

// served runs the worker over canned input and returns the replies.
func served(t *testing.T, mem *execMem, lines ...string) []reply {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	var out strings.Builder
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Attach: func() (*cli.Game, error) {
			if mem == nil {
				return nil, errors.New("no player found. Load into a world first.")
			}
			return &cli.Game{
				/*
					The test's own process stands in for the game.

					The worker drops its warm attachment when the pid is gone,
					which is how a game restart is noticed -- so a fixture with a
					pid that never existed re-attaches on every request and the
					whole point of the worker goes untested.
				*/
				Svc: service.New(mem, -1), Patcher: patch.NewPatcher(mem, -1),
				PID: os.Getpid(),
			}, nil
		},
	})
	require.NoError(t, app.Serve(t.Context(),
		strings.NewReader(strings.Join(lines, "\n")+"\n"), &out))

	var replies []reply
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r reply
		require.NoErrorf(t, json.Unmarshal([]byte(line), &r), "a reply is not JSON: %s", line)
		replies = append(replies, r)
	}
	return replies
}

// reply is one line the worker writes.
type reply struct {
	ID  json.RawMessage `json:"id"`
	OK  bool            `json:"ok"`
	Out string          `json:"out"`
}

func request(id int, argv ...string) string {
	raw, _ := json.Marshal(map[string]any{"id": id, "argv": argv})
	return string(raw)
}

// Every request is answered, with its own id, in order.
func TestTheWorkerAnswersEachRequestWithItsID(t *testing.T) {
	got := served(t, plantGame(),
		request(7, "status", "--json"),
		request(9, "inventory", "--all", "--json"))

	require.Len(t, got, 2)
	require.Equal(t, "7", string(got[0].ID))
	require.Equal(t, "9", string(got[1].ID))
	require.True(t, got[0].OK, "a status failed: %s", got[0].Out)
	require.Contains(t, got[0].Out, `"name":"Nakama"`, "the reply is not the status")
}

/*
Input ending ends the worker.

Without that a root process outlives the window that started it, which is the
one failure mode this design exists to avoid: an unprivileged panel and a
privileged helper that is gone the moment the panel is.
*/
func TestTheWorkerStopsWhenItsInputEnds(t *testing.T) {
	require.Empty(t, served(t, plantGame()), "an empty input produced replies")
}

/*
A command that is not on the list never runs.

The refusal is checked by its effect rather than by the message: the worker is
root with a JSON front door, and "it printed a refusal" is not the same claim as
"it did not do it".
*/
func TestUnallowedCommandsAreRefusedWithoutRunning(t *testing.T) {
	mem := plantGame()
	before := mem.Hex()

	got := served(t, mem,
		request(1, "freeze", "--godmode", "--seconds", "0.05"),
		request(2, "write", "0x10008738", "999"),
		request(3, "serve"),
		request(4))

	require.Len(t, got, 4)
	for i, r := range got {
		require.Falsef(t, r.OK, "request %d was allowed", i+1)
		require.Containsf(t, r.Out, "not served here", "request %d was refused for another reason", i+1)
	}
	require.Equal(t, before, mem.Hex(), "a refused command wrote to the game anyway")
}

// A malformed request is answered and forgotten rather than ending the worker.
func TestMalformedRequestsDoNotKillTheWorker(t *testing.T) {
	got := served(t, plantGame(),
		"not json at all",
		`{"id": 2, "argv": "notalist"}`,
		request(3, "status", "--json"))

	require.Len(t, got, 3)
	require.False(t, got[0].OK)
	require.False(t, got[1].OK)
	require.True(t, got[2].OK, "the worker stopped serving after bad input")
}

/*
A failure comes back with the marker the window keys on.

The window reads one merged stream, so a refusal printed to the error channel
has to arrive in the same string as the answer or it is simply lost.
*/
func TestFailuresComeBackAsErrorOutput(t *testing.T) {
	got := served(t, nil, request(1, "inventory", "--json"))
	require.Len(t, got, 1)
	require.False(t, got[0].OK)
	require.Contains(t, got[0].Out, "[ERROR]", "a failure carried no marker")
}

// And a command that runs and fails is reported as a failure, not as output.
func TestACommandThatFailsIsReportedAsOne(t *testing.T) {
	mem := plantGame()
	plantVersionInto(mem, "1.0.0.0")

	got := served(t, mem, request(1, "set-hp", "300"))
	require.Len(t, got, 1)
	require.False(t, got[0].OK, "a refused write was reported as having worked")
	require.Contains(t, got[0].Out, "[ERROR]")
}

/*
The warm attachment is made once and kept.

That is the whole point of the worker: a one-shot run pays a full memory scan
and a warm request pays a read.
*/
func TestTheWorkerAttachesOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := plantGame()
	attached := 0

	var out strings.Builder
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Attach: func() (*cli.Game, error) {
			attached++
			return &cli.Game{
				/*
					The test's own process stands in for the game.

					The worker drops its warm attachment when the pid is gone,
					which is how a game restart is noticed -- so a fixture with a
					pid that never existed re-attaches on every request and the
					whole point of the worker goes untested.
				*/
				Svc: service.New(mem, -1), Patcher: patch.NewPatcher(mem, -1),
				PID: os.Getpid(),
			}, nil
		},
	})
	lines := make([]string, 0, 5)
	for i := range 5 {
		lines = append(lines, request(i, "status", "--json"))
	}
	require.NoError(t, app.Serve(t.Context(),
		strings.NewReader(strings.Join(lines, "\n")+"\n"), &out))

	require.Equal(t, 1, attached, "the worker re-attached for every request")
	require.Equal(t, 5, strings.Count(strings.TrimSpace(out.String()), "\n")+1,
		"a request went unanswered")
}

// A reply carries whatever id came in, including one that is not a number.
func TestAnIDIsEchoedWhateverItIs(t *testing.T) {
	got := served(t, plantGame(),
		`{"id": "seven", "argv": ["status", "--json"]}`,
		`{"argv": ["status", "--json"]}`)

	require.Len(t, got, 2)
	require.Equal(t, `"seven"`, string(got[0].ID), "a string id was not echoed")
	require.Equal(t, "null", string(got[1].ID), "a missing id became something else")
}

var _ = fmt.Sprint // the helpers above build requests by hand

/*
The warm attachment is dropped when the game it was talking to has gone.

A restart is a new process, and the addresses in the old one belong to memory
somebody else may be using now. Without this the worker keeps writing to them
and reports that it worked.
*/
func TestTheWorkerReattachesWhenTheGameRestarts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mem := plantGame()
	attached, there := 0, true

	var out strings.Builder
	app := cli.NewApp(cli.Options{
		Elevate: func() error { return nil },
		Alive:   func(int) bool { return there },
		Attach: func() (*cli.Game, error) {
			attached++
			return &cli.Game{
				Svc: service.New(mem, -1), Patcher: patch.NewPatcher(mem, -1),
				PID: os.Getpid(),
			}, nil
		},
	})
	require.NoError(t, app.Serve(t.Context(),
		strings.NewReader(request(1, "status", "--json")+"\n"), &out))
	require.Equal(t, 1, attached)

	there = false
	require.NoError(t, app.Serve(t.Context(),
		strings.NewReader(request(2, "status", "--json")+"\n"), &out))
	require.Equal(t, 2, attached, "the worker kept talking to a game that had gone")
}
