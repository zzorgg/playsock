# Playsock Matchmaking Server

A realtime WebSocket server written in Go that pairs mobile players into head-to-head trivia matches, relays game state updates, and enforces basic multiplayer rules. SOL wagers are handled entirely on-chain by the clients; the server simply mirrors bet metadata so both players can settle through Anchor afterwards.

## Features

- Accepts WebSocket connections from Kotlin (or any) clients at `GET /ws`.
- Maintains a matchmaking queue and automatically pairs the next two compatible players (optionally filtered by bet token/amount).
- Optionally mirrors queue and session metadata into Valkey so multiple server instances share visibility and analytics.
- Creates lightweight game sessions that track both players, their scores, round information, and bet metadata.
- Relays score updates and opponent answers in realtime.
- Detects disconnects and awards a walkover victory to the remaining player.
- Sends end-of-match summaries for clients to validate before finalising Anchor transactions.
  

## Quick start

```bash
# Run the server (defaults to :8080 and accepts all origins)
go run ./...
```

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PLAYSOCK_ADDR` | `:8080` | HTTP bind address. |
| `PLAYSOCK_ALLOWED_ORIGINS` | *(accept all)* | Comma-separated list of exact origins allowed to upgrade. For native mobile clients, supply the scheme/host they emit (e.g. `playsock-mobile://app`). Leave empty or set to `*` only when your clients cannot set an Origin header. |
| `PLAYSOCK_VALKEY_ADDR` | *(disabled)* | Valkey connection string (`host:port`). Enables shared matchmaking metadata. |
| `PLAYSOCK_VALKEY_USERNAME` | *(empty)* | Optional username for ACL authentication. |
| `PLAYSOCK_VALKEY_PASSWORD` | *(empty)* | Optional password/ACL token. |
| `PLAYSOCK_VALKEY_DB` | `0` | Numeric logical database index. |
| `PLAYSOCK_VALKEY_QUEUE_KEY` | `playsock:queue` | Hash key used to track queued players. |
| `PLAYSOCK_VALKEY_SESSION_PREFIX` | `playsock:session` | Key prefix for active session snapshots. |
| `PLAYSOCK_VALKEY_TIMEOUT` | `2s` | Operation timeout (parseable by `time.ParseDuration`). |
| `PLAYSOCK_QUEUE_TIMEOUT` | `5m` | Max time a player can remain queued before being timed out. |
| `PLAYSOCK_SHUTDOWN_TIMEOUT` | `10s` | Grace period for draining active connections during shutdown. |

When Valkey is configured the server writes each queue join/remove into `PLAYSOCK_VALKEY_QUEUE_KEY` and snapshots active sessions under `PLAYSOCK_VALKEY_SESSION_PREFIX:<match_id>`.

## WebSocket contract

All frames are UTF-8 JSON encoded envelopes:

```json
{
  "action": "join_queue",
  "data": { /* action-specific payload */ }
}
```

### Client → Server actions

| Action | Payload | Purpose |
|--------|---------|---------|
| `join_queue` | `{ "player_id": "p1", "display_name": "Alice", "bet_amount": 1.5, "bet_token": "SOL" }` | Registers/updates the player and enters them into the matchmaking queue. |
| `submit_answer` | `{ "match_id": "...", "player_id": "p1", "question_id": "q1", "answer": "42", "correct": true, "score_delta": 1, "final": false, "round_number": 1 }` | Reports a round result. The server updates internal scores and forwards the outcome. |

### Server → Client actions

| Action | Payload | Description |
|--------|---------|-------------|
| `queued` | `{ "position": 1 }` | Confirmation that the player joined the queue. |
| `match_found` | `{ "match_id": "...", "player_id": "p1", "opponent_id": "p2", "opponent_name": "Bob", "bet_amount": 1.5, "bet_token": "SOL", "queue_delta_ms": 12 }` | Two players were paired; both are notified simultaneously. `queue_delta_ms` shows the millisecond gap between the players' join events. |
| `score_update` | `{ "match_id": "...", "scores": {"p1": 1, "p2": 0}, "updated_player_id": "p1", "correct": true, "question_id": "q1", "round_number": 1 }` | Current scoreboard after an answer. |
| `opponent_answer` | `{ "match_id": "...", "player_id": "p1", "question_id": "q1", "answer": "42", "round_number": 1, "correct": true }` | Optional mirror of the opponent's submission for richer UX. |
| `opponent_left` | `{ "match_id": "...", "opponent_id": "p2" }` | Sent if the opponent disconnects mid-match. |
| `game_over` | `{ "match_id": "...", "winner_id": "p1", "reason": "final_round", "scores": {"p1": 5, "p2": 3}, "bet_amount": 1.5, "bet_token": "SOL" }` | Match finished—use this info to settle on-chain. |
| `error` | `{ "message": "..." }` | Validation feedback (e.g., malformed payloads). |

## Development workflow

- Format and lint: `gofmt -w .`
- Run tests: `go test ./...`

Unit tests cover matchmaking, score tracking, and disconnect handling logic without requiring live WebSocket connections.

## Matchmaking algorithm

The lobby keeps an ordered, concurrency-safe queue. Each time a player taps “Play”, the server records:

- The precise `time.Now()` timestamp (monotonic on supported platforms).
- A monotonically increasing sequence number to break ties when two players arrive within the same millisecond.

Players are inserted into the queue in ascending timestamp/sequence order. Whenever a player joins, the lobby scans from the head of the queue to find the earliest compatible opponent (bet amount/token). Once a pair is found:

1. Both players are atomically popped from the queue.
2. A new match is created with a UUIDv4 `match_id`, and both clients share that identifier for the rest of the session and any settlement logic.
3. The millisecond delta between the two click times is captured and returned in the `match_found` payload as `queue_delta_ms` (and logged) so operators can audit fairness.
4. If either player disconnects, the server broadcasts `opponent_left` followed by `game_over` including the same `match_id`, ensuring the survivor and any observers can close out the match cleanly.

This strategy guarantees “first click, first served” ordering even under heavy concurrency, while remaining simple enough to remain fast at small queue sizes. When Valkey is configured the queue and session mutations are mirrored so multiple pods stay in sync.

## Docker

The repository ships with a production-ready multi-stage `Dockerfile`. To build and run the container locally:

```bash
docker build -t playsock:latest .
docker run --rm -p 8080:8080 \
  -e PLAYSOCK_ALLOWED_ORIGINS="https://your-frontend.example" \
  playsock:latest
```

For local development with Valkey, use the provided Compose file:

```bash
docker compose up --build
```

The container runs as a non-root user and exposes port `8080`. Health checks hit `/healthz` and can be wired into orchestrators such as DigitalOcean App Platform, Kubernetes, or Docker Swarm.
