# yoyodyne-ifd.431.1: the sink hung up on its own healthy connection every ninety seconds, and Slack counted the wreckage

The finding, as raised: the Slack sink's process logged about one
`connection dropped; reconnecting` line a minute for thirty hours, each of them
a read timeout on the Socket Mode websocket, while the sink's own hello warning
said the workspace was holding between three and ten Socket Mode connections
for an app that had one process running. Two readings were offered — a
reconnect that does not close its predecessor, or connections that outlive the
process at Slack's end — and neither was diagnosed.

It is the second, and the first is not happening at all. But the thing that fed
it is neither: the sink was killing its own connections on a timer of its own
making, and Slack was still counting them when the next one opened.

## What the log says

Read from the sink's own log at
`<state>/products/yoyodyne/slack/sink.log` on 2026-09-22, which spans five
process lifetimes:

| | |
|---|---|
| Connections opened (`connected to Slack over Socket Mode`) | 1,994 |
| Connections dropped with a reported cause | 1,988 |
| Of those, `i/o timeout` on a read | **1,988** |
| Of those, a network error, a refusal, or anything else | **0** |
| Permanent refusals (`refused and will stay refused`) | 0 |

Every drop, without exception, is the client's own read deadline expiring:

```text
the Slack connection dropped; reconnecting in 1m0s: read websocket frame: read tcp 192.168.21.187:50038->44.235.135.203:443: i/o timeout
```

1,897 of the 1,988 say `1m0s`, which is the reconnection backoff pinned at its
ceiling. The backoff resets only when a session ends in an orderly close, and
across five process lifetimes it reset about thirty times. Slack was almost
never the one hanging up.

## Why the reads timed out

`socketReadTimeout` is ninety seconds, and the sink asked for it like this:

```go
raw, err := socket.ReadMessage(time.Now().Add(timeout))
```

`ReadMessage` set that deadline once, at the top, and then looped over frames
until a *data* message completed. A Socket Mode connection with nothing
happening on it carries no data messages at all — Slack keeps it alive with
websocket ping control frames, which `ReadMessage` answered inside that loop
without ever touching the deadline. So the bound was never on silence. It was a
bound on how long a workspace was allowed to be quiet, and a quiet workspace is
the ordinary state of a product with nobody typing in its channel.

Ninety seconds of nobody typing, and the sink hung up on a connection whose
pings were proving, every few seconds, that it was healthy. Then it backed off
sixty seconds and opened another. That is the minute-by-minute cadence in the
log, and it is entirely self-inflicted: the connection was fine.

## Why Slack held three to ten

Nothing in this process holds two connections. `run` calls `session` one at a
time and `session` closes its socket on every path out of it, so the surplus
connections were not ones this process still had open — they were ones Slack
had not finished retiring when the next one arrived. **The leak is at Slack's
end**, which is the branch the work item allowed for, and the reason it
mattered is that the sink was feeding it at one connection every hundred and
fifty seconds.

The log shows the consequence rather than the accumulation. `num_connections`
is only reported on hello, and the counts never climb through the middle of the
range; they appear at ten and then decay:

```text
10 10 10 9 8 8 8 7 7 7 6 6 6 6 5 5 4 3 3 3 3 3 3 3 2 2 2
10 10 10 10 10 9 9 8 8 8 …
```

Ten is Slack's limit on Socket Mode connections for one app. Each of those
runs of tens is the sink sitting against its cap, and every one of them is
accompanied by the backoff resetting — which is to say by a session ending in
an orderly close rather than a timeout. That is Slack closing the oldest
connection to make room for the new one. The app spent thirty hours being
disconnected by its own reconnects.

What it cost was headroom and nothing else, so far. The failure it was heading
for is the one the item names: at the cap, with no connection to retire, the
sink stops being able to connect and reporting stops without anything saying
so.

## What changed

- **The read bound is on silence, not on quiet.** `ReadMessage` takes an idle
  duration and renews the read deadline before every frame, so a ping renews it
  exactly as an event does. A peer that has gone away still sends nothing at
  all and is still given up on after ninety seconds; a quiet workspace is no
  longer mistaken for one. This is the cause fix, and on its own it takes the
  connection count for an idle sink from about five hundred a day to however
  many times Slack asks for a refresh.
- **Hanging up says so.** `websocketConn.Close` now sends a websocket close
  frame before closing the transport, bounded at 250ms so a peer that has
  stopped reading cannot hold a hang-up open. Slack counts a connection until
  it sees the connection end, and a frame saying so is what ends it at once
  instead of leaving Slack's own timer to notice.
- **Closing is synchronous.** A second `Close` now waits for the first rather
  than returning while the socket is still open, so "the predecessor is closed
  before the replacement is opened" is a fact a test can assert rather than a
  race it can usually win.
- **The warning names the bound.** The hello warning said only that more than
  one sink opens more than one thread. It now says how many of the ten Slack
  allows are held, that this process holds one and closes it before opening
  another, and that at the limit Slack closes the oldest — so an operator
  reading it knows what the number is out of and what happens when it is
  reached.

`TestPingsAloneKeepAQuietConnectionAlive` drives pings spanning four times the
idle bound and fails with the production signature — `i/o timeout` — against
the old deadline. `TestAReconnectClosesTheConnectionItReplaces` drives a
refresh through `run` and asserts the first connection's peer saw it end before
the second was opened.

## Not fixed here, and worth admitting

The same log carries 7,361 repetitions of

```text
this pass over the records could not finish; the cursors are unchanged and it will be retried: read the recorded runs: discover recorded runs: decode run state run-8550a2e9d4f77d956e6fda09b59bdec5: json: unknown field "work_item_labels"
```

over five distinct run states, each of them a record a newer binary wrote and
the running sink cannot decode — `work_item_labels`, `stale_block_clear`, and
an environmental cause named `process-vanished` that this harness does not
recognise. `Sink.pass` takes the whole poll as one thing, so a single
undecodable run state aborts the pass for every stream and leaves every cursor
where it was. Nothing is posted for as long as the record is there to be read,
and the only sign of it is this line. Reporting stopping because one file is
newer than the reader is a separate defect from this one and is not fixed here.
