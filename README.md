# Playsock Matchmaking Server

A realtime WebSocket server written in Go that pairs mobile players into head-to-head trivia matches, relays game state updates, and enforces basic multiplayer rules. SOL wagers are handled entirely on-chain by the clients; the server simply mirrors bet metadata so both players can settle through Anchor afterwards.

## Features

- Accepts WebSocket connections from Kotlin (or any) clients at `GET /ws`.
- Maintains a matchmaking queue and automatically pairs the next two compatible players (optionally filtered by bet token/amount).
- Optionally mirrors queue and session metadata into Redis so multiple server instances share visibility and analytics.
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
| `PLAYSOCK_ALLOWED_ORIGINS` | *(accept all)* | Comma-separated list of exact origins allowed to upgrade. Leave empty during development. |
| `PLAYSOCK_REDIS_ADDR` | *(disabled)* | Redis connection string (`host:port`). When set, matchmaking metadata is mirrored into Redis. |
| `PLAYSOCK_REDIS_USERNAME` | *(empty)* | Optional username for Redis ACL authentication. |
| `PLAYSOCK_REDIS_PASSWORD` | *(empty)* | Optional password/ACL token. |
| `PLAYSOCK_REDIS_DB` | `0` | Numeric Redis database index. |
| `PLAYSOCK_REDIS_QUEUE_KEY` | `playsock:queue` | Hash key used to track queued players. |
| `PLAYSOCK_REDIS_SESSION_PREFIX` | `playsock:session` | Key prefix for active session snapshots. |
| `PLAYSOCK_REDIS_TIMEOUT` | `2s` | Operation timeout (parseable by `time.ParseDuration`). |

When Redis is configured the server writes each queue join/remove into `PLAYSOCK_REDIS_QUEUE_KEY` and snapshots active sessions under `PLAYSOCK_REDIS_SESSION_PREFIX:<match_id>`. This keeps per-player WebSocket state locally while sharing high-level metadata across nodes.

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
| `match_found` | `{ "match_id": "...", "player_id": "p1", "opponent_id": "p2", "opponent_name": "Bob", "bet_amount": 1.5, "bet_token": "SOL" }` | Two players were paired; both are notified simultaneously. |
| `score_update` | `{ "match_id": "...", "scores": {"p1": 1, "p2": 0}, "updated_player_id": "p1", "correct": true, "question_id": "q1", "round_number": 1 }` | Current scoreboard after an answer. |
| `opponent_answer` | `{ "match_id": "...", "player_id": "p1", "question_id": "q1", "answer": "42", "round_number": 1, "correct": true }` | Optional mirror of the opponent's submission for richer UX. |
| `opponent_left` | `{ "match_id": "...", "opponent_id": "p2" }` | Sent if the opponent disconnects mid-match. |
| `game_over` | `{ "match_id": "...", "winner_id": "p1", "reason": "final_round", "scores": {"p1": 5, "p2": 3}, "bet_amount": 1.5, "bet_token": "SOL" }` | Match finished—use this info to settle on-chain. |
| `error` | `{ "message": "..." }` | Validation feedback (e.g., malformed payloads). |

## Development workflow

- Format and lint: `gofmt -w .`
- Run tests: `go test ./...`

Unit tests cover matchmaking, score tracking, and disconnect handling logic without requiring live WebSocket connections.
