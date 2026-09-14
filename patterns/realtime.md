# Realtime

> Long-lived connections, server-side fanout, and reconnection that does not
> lose messages.

This layer applies when clients must see changes without asking — chat,
presence, live counters, collaborative state. It is a separate concern from
[htmx.md](htmx.md), which covers request-driven interactivity, and usually a
separate **process**.

## Table of Contents

- [The Central Rule](#the-central-rule)
- [Process Shape](#process-shape)
- [Transport Choice](#transport-choice)
- [Connection Lifecycle](#connection-lifecycle)
- [Ordering](#ordering)
- [Fanout Across Processes](#fanout-across-processes)
- [Reconnection and Resume](#reconnection-and-resume)
- [Backpressure](#backpressure)
- [Presence](#presence)
- [Authorization](#authorization)
- [Testing](#testing)
- [Operating It](#operating-it)

---

## The Central Rule

**The socket is a hint. The database is the truth.**

A push tells a client that something changed. It is not the delivery mechanism
for the change itself, and it is never the only path by which a client can learn
something. Every client can reconstruct complete, correct state from an HTTP
read with a cursor, and does so on connect, on reconnect, and on any gap it
detects.

Accept this and the hard problems dissolve: a dropped frame is a latency
problem, not a data-loss bug; you do not need exactly-once delivery over a
network that cannot provide it; a client on a train that loses signal for twenty
minutes recovers by asking, not by you having buffered twenty minutes of frames
for it.

Reject it and you will spend months on per-connection durable queues and still
lose messages.

---

## Process Shape

The gateway is its own binary:

```
cmd/
├── api/       # HTTP: writes, reads, everything request-shaped
├── gateway/   # WebSocket: connections, fanout
└── worker/    # jobs
```

They share `internal/` and the database. The gateway is separate because its
resource profile is nothing like the API's — thousands of idle connections
holding memory and file descriptors, near-zero CPU, and a deploy that
disconnects everyone. You want to scale and restart it independently.

**The gateway does not write domain data.** Clients send writes over HTTP to the
API. The gateway's only job is delivering notifications outward. A write path
over the socket means duplicating authorization, validation, and idempotency in
a second place, with different failure semantics.

The one thing clients send upward is subscription intent ("I am looking at
channel 7") and heartbeats.

---

## Transport Choice

| Transport | Use when |
|---|---|
| **SSE** | Server→client only. Plain HTTP, works through every proxy, auto-reconnects natively, trivially supported by the HTMX SSE extension. |
| **WebSocket** | You need client→server frames, or per-connection subscription changes at high frequency. |
| **Long polling** | Fallback only. |

**Start with SSE.** It covers chat delivery completely, and a large fraction of
products that reached for WebSocket needed one direction. You can add a socket
later without changing the fanout layer, because both sit behind the same hub.

Use WebSocket when you genuinely need upstream frames — typing indicators,
cursor positions, anything at a frequency that would be silly as HTTP requests.
Below, `Conn` is transport-agnostic.

---

## Connection Lifecycle

```go
// internal/gateway/hub.go
package gateway

type Conn struct {
  ID        string
  UserID    int64
  TenantID  int64
  send      chan []byte     // bounded — see Backpressure
  channels  map[int64]bool  // what this connection is subscribed to
  expiresAt time.Time       // from the session; re-auth before this
  mu        sync.RWMutex
}

type Hub struct {
  mu       sync.RWMutex
  conns    map[string]*Conn
  byUser   map[int64]map[string]*Conn  // fanout to a person on N devices
  byTenant map[int64]map[string]*Conn
}
```

**Authenticate at connect, before the upgrade.** The handshake is an ordinary
HTTP request and carries the session cookie; reject there and you never allocate
a connection. Do not accept an unauthenticated socket intending to authenticate
in the first frame — you have just allowed anonymous clients to hold resources.

**Re-authenticate on expiry.** A connection open for eight hours outlives its
session. Store `expiresAt` and close when it passes; the client reconnects and
re-authenticates. A socket is not a permanent authorization.

**Heartbeat both ways.** TCP will not tell you about a laptop that slept or a
NAT that dropped the mapping. The server pings on an interval and closes on a
missed pong; the client reconnects when its own timer sees silence. Without
this, the gateway accumulates dead connections and the user sees a UI that looks
live and is frozen.

```go
const (
  pingEvery   = 25 * time.Second   // under typical 30s proxy idle timeouts
  pongTimeout = 60 * time.Second
  writeWait   = 10 * time.Second
)
```

Set `pingEvery` below the idle timeout of every proxy in the path. A load
balancer closing idle connections at 30s while you ping at 60s produces a
reconnect loop that looks like a client bug.

---

## Ordering

**Every event carries a server-assigned monotonic sequence, scoped to its
channel.** Not a timestamp — clocks are not monotonic, and two messages in the
same millisecond are common.

```sql
CREATE TABLE message (
  id         BIGSERIAL PRIMARY KEY,
  tenant_id  BIGINT NOT NULL REFERENCES tenant(id),
  channel_id BIGINT NOT NULL REFERENCES channel(id),
  seq        BIGINT NOT NULL,          -- per-channel, gapless, assigned on write
  body       TEXT   NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX uq_message_channel_seq ON message (channel_id, seq);
```

Assign it in the insert, inside the transaction:

```sql
INSERT INTO message (tenant_id, channel_id, seq, body)
SELECT $1, $2, COALESCE(MAX(seq), 0) + 1, $3 FROM message WHERE channel_id = $2
RETURNING id, seq;
```

This serialises writes per channel, which is exactly the guarantee you want and
a non-issue at chat volumes. A per-channel counter row with `UPDATE ...
RETURNING` is equivalent. A global sequence is not — it gives clients no way to
detect a gap in the channel they are actually reading.

The sequence is what makes the whole design work. A client that receives seq 41
having last seen 39 knows it missed 40, and refetches. Without it, it cannot
distinguish a dropped message from a message not yet sent.

---

## Fanout Across Processes

With N gateway processes, a write handled by API instance 2 must reach a
connection held by gateway instance 5.

### Postgres LISTEN/NOTIFY

```go
err := tx.WithContext(ctx).Exec(`SELECT pg_notify(?, ?)`,
  "channel_"+strconv.FormatInt(channelID, 10), payload).Error
```

- **For:** no new infrastructure; fires on commit, so subscribers never see a
  notification for an uncommitted row.
- **Against:** 8000-byte payload limit; needs a dedicated connection per
  listener (it cannot share a pool); delivery is at-most-once with no buffering —
  a `NOTIFY` sent while nobody listens is gone.

Good to perhaps a few thousand events/sec. Correct for most products.

### Redis pub/sub

- **For:** scales further, no payload limit, one connection multiplexes all
  channels, and you likely already run Redis for sessions and rate limiting.
- **Against:** another dependency; fire-and-forget, so a publish that races a
  subscriber's reconnect is lost; no transactional coupling to the commit.

### Which

Both are lossy, and **neither needs to be reliable**, because of
[The Central Rule](#the-central-rule). Start with `LISTEN/NOTIFY`, move to Redis
when you measure a reason. Put both behind one interface:

```go
type Broker interface {
  Publish(ctx context.Context, topic string, payload []byte) error
  Subscribe(ctx context.Context, topics ...string) (<-chan Event, error)
}
```

**Publish the pointer, not the payload.** Send `{channel_id, seq, message_id}`
and let the client read the body if it needs it. This stays inside NOTIFY's
limit, avoids serialising the same body N times, and sidesteps a whole class of
bug where a user is pushed content they are no longer permitted to see —
authorization is re-checked on the read.

Publish **after commit**, in the same transaction if using `pg_notify` (which
Postgres defers to commit for you), or in a `defer` that runs only on success.
Publishing before commit means clients fetch a row that does not exist yet.

---

## Reconnection and Resume

The pattern that makes a chat client correct:

1. Client connects, sending its last known `seq` per subscribed channel.
2. Gateway replies with nothing, or a `gap` marker if the client is behind.
3. Client fetches `GET /api/channels/:id/messages?after_seq=N` over HTTP.
4. Client applies, then processes live frames.
5. Any live frame whose `seq` is not `last+1` triggers step 3 again.

```js
socket.onmessage = (e) => {
  const evt = JSON.parse(e.data);
  const expected = lastSeq[evt.channel_id] + 1;
  if (evt.seq > expected) {
    fetchSince(evt.channel_id, lastSeq[evt.channel_id]);  // gap: reconcile
    return;
  }
  if (evt.seq < expected) return;                          // duplicate: ignore
  apply(evt);
  lastSeq[evt.channel_id] = evt.seq;
};
```

The client tolerates duplicates, detects gaps, and never assumes the socket
delivered everything. The server keeps no per-client state, holds no replay
buffer, and can drop any frame under pressure without correctness consequence.

Reconnect with **exponential backoff and jitter**, capped around 30s. A gateway
restart disconnects every client simultaneously; without jitter they all return
in the same instant and knock it over again.

---

## Backpressure

A slow consumer — a throttled mobile connection, a suspended laptop — must not
block the writer.

```go
func (c *Conn) trySend(b []byte) {
  select {
  case c.send <- b:
  default:
    // Buffer full. Drop the connection, not the process.
    metrics.ConnEvicted.Inc()
    c.CloseWithReason("slow consumer")
  }
}
```

**Never block on a per-connection channel while holding the hub lock**, and
never let the buffer grow unbounded. An unbounded send buffer turns one stalled
client into an OOM.

Closing is safe precisely because of the resume protocol: the client reconnects
and refetches. Evicting a slow consumer costs it a round trip and costs the
system nothing. Buffering for it costs the system memory proportional to the
worst client on the network.

Size the buffer small — 32 to 256 frames. Large buffers do not fix slow
consumers; they delay the eviction and make the eventual recovery worse.

---

## Presence

Presence is ephemeral, high-churn, and worthless when stale. **It does not go in
Postgres.** A row per user per connection, updated every few seconds, is a write
storm against your most important table for data nobody will miss.

Redis with a TTL:

```go
// refreshed every 20s by the gateway while the connection lives
rdb.Set(ctx, fmt.Sprintf("presence:%d:%d", tenantID, userID), gatewayID,
        45*time.Second)
```

Expiry handles disconnects, crashes, and network partitions with no cleanup
path. If Redis is not available, derive presence from "has an open connection on
this gateway" and accept that it is per-process — usually good enough, and
honest about it.

Do not push presence changes to every connection in a tenant. A 200-person
workspace at morning standup produces 40,000 frames in a minute. Batch on an
interval and send diffs, or let clients poll presence on a timer. Presence is
the feature most likely to melt your gateway, and it is the least important one.

---

## Authorization

**Check on subscribe, and check again on read.** A connection authenticated an
hour ago may since have been removed from a channel or from the workspace.

Because you [publish pointers, not payloads](#fanout-across-processes), the
authoritative check happens when the client fetches the body over HTTP, through
the ordinary tenant-scoped path in [database.md](database.md#multi-tenancy).
The gateway's subscribe check is an optimisation that avoids waking clients
pointlessly; it is not the security boundary.

Two rules regardless:

- Subscription topics are derived from the session, never from client input. A
  client asking to subscribe to `channel_9` must be checked against its
  membership; a client that can name a topic can otherwise read any of them.
- On membership revocation, close or re-scope affected connections. Do not wait
  for expiry.

---

## Testing

The gateway is testable without a network:

```go
func TestGapTriggersRefetch(t *testing.T) {
  hub := gateway.NewHub()
  c := hub.AddTestConn(userID, tenantID)
  hub.Subscribe(c, channelID)

  hub.Publish(gateway.Event{ChannelID: channelID, Seq: 5})
  hub.Publish(gateway.Event{ChannelID: channelID, Seq: 7}) // 6 missing

  assert.Equal(t, []int64{5, 7}, c.Received())
  // the client, not the server, reconciles -- assert in the client suite
}
```

Worth testing explicitly:

- A full send buffer evicts the connection and does not block the publisher.
- A connection past `expiresAt` is closed.
- A missed pong closes the connection.
- Subscribing to a channel the user does not belong to is refused.
- Two connections for one user both receive.
- `seq` is gapless and unique per channel under concurrent inserts (run it with
  `-race` and real concurrency; this is where the counter design fails if it is
  going to).

Load-test the gateway separately and early, with idle connections rather than
busy ones. The failure mode is 20,000 sockets doing nothing, not 200 doing a lot,
and you want to know your per-connection memory cost before launch.

---

## Operating It

| Metric | Watch for |
|---|---|
| `ws_connections_active` | Against per-process fd and memory limits |
| `ws_connections_evicted_total` | Rising — buffers too small or network genuinely bad |
| `ws_fanout_latency_seconds` | Commit to client-received; the number users feel |
| `ws_reconnects_total` | Spikes mean proxy timeouts or a restart loop |
| `ws_gap_refetch_total` | Rising — fanout is dropping more than it should |

Raise the file descriptor limit (`LimitNOFILE` in the systemd unit): one
connection is one fd, and the default 1024 is reached quickly.

On deploy, drain: stop accepting new connections, send a `reconnect` frame with
a jittered delay, then close. Clients then reconnect spread over seconds instead
of stampeding the new process the moment it binds.
