package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLobbyPairing(t *testing.T) {
	lobby := NewLobby(0, nil)

	c1 := &Client{id: "alice", name: "Alice", send: make(chan []byte, 4)}
	c2 := &Client{id: "bob", name: "Bob", send: make(chan []byte, 4)}

	if pos, session := lobby.enqueue(c1); session != nil || pos != 1 {
		t.Fatalf("first enqueue should put player in queue; got pos=%d session=%v", pos, session)
	}

	if pos, session := lobby.enqueue(c2); session == nil {
		t.Fatalf("second enqueue should create a session")
	} else {
		if pos != 0 {
			t.Fatalf("expected returned position 0 when session formed, got %d", pos)
		}
		if len(session.players) != 2 {
			t.Fatalf("session should have 2 players, got %d", len(session.players))
		}
		if session.players["alice"].matchID != session.ID {
			t.Fatalf("alice match id not set")
		}
		if session.players["bob"].matchID != session.ID {
			t.Fatalf("bob match id not set")
		}
		if session.queueGap < 0 {
			t.Fatalf("queue gap should be non-negative, got %v", session.queueGap)
		}
	}
}

func TestHandleAnswerUpdatesScoresAndRelays(t *testing.T) {
	lobby := NewLobby(0, nil)

	c1 := &Client{id: "alice", name: "Alice", send: make(chan []byte, 4)}
	c2 := &Client{id: "bob", name: "Bob", send: make(chan []byte, 4)}

	session := newSession(c1, c2)
	lobby.sessions = map[string]*Session{session.ID: session}

	payload := submitAnswerPayload{
		MatchID:     session.ID,
		PlayerID:    "alice",
		QuestionID:  "q1",
		Answer:      "42",
		Correct:     true,
		RoundNumber: 1,
		Final:       false,
	}

	if err := lobby.handleAnswer(payload); err != nil {
		t.Fatalf("handleAnswer error: %v", err)
	}

	if session.scores["alice"] != 1 {
		t.Fatalf("expected alice score 1, got %d", session.scores["alice"])
	}

	// Bob should get a relay message.
	select {
	case msg := <-c2.send:
		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if env.Action != ActionScoreUpdate {
			t.Fatalf("expected first message score_update, got %s", env.Action)
		}
	default:
		t.Fatalf("bob did not receive score update")
	}

	select {
	case msg := <-c2.send:
		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if env.Action != ActionRelayAnswer {
			t.Fatalf("expected second message opponent_answer, got %s", env.Action)
		}
	default:
		t.Fatalf("bob did not receive opponent answer relay")
	}
}

func TestHandleDisconnectAwardsWin(t *testing.T) {
	lobby := NewLobby(0, nil)

	c1 := &Client{id: "alice", name: "Alice", send: make(chan []byte, 4)}
	c2 := &Client{id: "bob", name: "Bob", send: make(chan []byte, 4)}

	session := newSession(c1, c2)
	session.scores["alice"] = 3
	session.scores["bob"] = 5

	lobby.sessions = map[string]*Session{session.ID: session}

	lobby.handleDisconnect(c1, "network")

	if _, ok := lobby.sessions[session.ID]; ok {
		t.Fatalf("session should be removed after disconnect")
	}

	if c1.matchID != "" {
		t.Fatalf("alice matchID should be cleared")
	}
	if c2.matchID != "" {
		t.Fatalf("bob matchID should be cleared")
	}

	// Bob should receive opponent_left and game_over messages.
	var actions []string
	for i := 0; i < 2; i++ {
		select {
		case msg := <-c2.send:
			var env Envelope
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			actions = append(actions, env.Action)
		default:
			t.Fatalf("missing expected message %d", i)
		}
	}

	if actions[0] != ActionOpponentLeft && actions[1] != ActionOpponentLeft {
		t.Fatalf("expected opponent_left message, got %v", actions)
	}
	if actions[0] != ActionGameOver && actions[1] != ActionGameOver {
		t.Fatalf("expected game_over message, got %v", actions)
	}
}

type fakeValkeyStore struct {
	queueAdds      []valkeyQueueEntry
	queueRemoves   []string
	sessionSaves   []valkeySessionRecord
	sessionDeletes []string
}

func (f *fakeValkeyStore) queueAdd(entry valkeyQueueEntry) error {
	f.queueAdds = append(f.queueAdds, entry)
	return nil
}

func (f *fakeValkeyStore) queueRemove(playerID string) error {
	f.queueRemoves = append(f.queueRemoves, playerID)
	return nil
}

func (f *fakeValkeyStore) sessionSave(record valkeySessionRecord) error {
	f.sessionSaves = append(f.sessionSaves, record)
	return nil
}

func (f *fakeValkeyStore) sessionDelete(matchID string) error {
	f.sessionDeletes = append(f.sessionDeletes, matchID)
	return nil
}

func TestLobbyPersistsToValkeyStore(t *testing.T) {
	store := &fakeValkeyStore{}
	lobby := NewLobby(0, store)

	c1 := &Client{id: "alice", name: "Alice", send: make(chan []byte, 4)}
	c2 := &Client{id: "bob", name: "Bob", send: make(chan []byte, 4)}

	if _, session := lobby.enqueue(c1); session != nil {
		t.Fatalf("not expecting session with single player")
	}
	if len(store.queueAdds) != 1 {
		t.Fatalf("expected queueAdd to be called once, got %d", len(store.queueAdds))
	}

	_, session := lobby.enqueue(c2)
	if session == nil {
		t.Fatalf("expected session to be created when second player joins")
	}
	if len(store.sessionSaves) == 0 {
		t.Fatalf("expected sessionSave when session created")
	}

	payload := submitAnswerPayload{
		MatchID:     session.ID,
		PlayerID:    "alice",
		QuestionID:  "q1",
		Answer:      "42",
		Correct:     true,
		RoundNumber: 1,
	}

	if err := lobby.handleAnswer(payload); err != nil {
		t.Fatalf("handleAnswer failed: %v", err)
	}
	if len(store.sessionSaves) < 2 {
		t.Fatalf("expected sessionSave after score update")
	}

	lobby.handleDisconnect(c1, "quit")
	if len(store.sessionDeletes) == 0 {
		t.Fatalf("expected sessionDelete to be called")
	}

	foundAlice := false
	for _, id := range store.queueRemoves {
		if id == "alice" {
			foundAlice = true
			break
		}
	}
	if !foundAlice {
		t.Fatalf("expected queueRemove to be called with alice")
	}
}

func TestMatchFoundPayloadCarriesQueueDelta(t *testing.T) {
	lobby := NewLobby(0, nil)

	c1 := &Client{id: "alice", name: "Alice", send: make(chan []byte, 8)}
	c2 := &Client{id: "bob", name: "Bob", send: make(chan []byte, 8)}

	if _, session := lobby.enqueue(c1); session != nil {
		t.Fatalf("unexpected session for first enqueue")
	}

	time.Sleep(5 * time.Millisecond)

	if _, session := lobby.enqueue(c2); session == nil {
		t.Fatalf("expected session when second player joins")
	} else {
		lobby.notifyMatchFound(session)
	}

	select {
	case msg := <-c1.send:
		var env Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		if env.Action != ActionMatchFound {
			t.Fatalf("expected match_found action, got %s", env.Action)
		}
		var payload matchFoundPayload
		if err := json.Unmarshal(env.Data, &payload); err != nil {
			t.Fatalf("invalid payload: %v", err)
		}
		if payload.QueueDeltaMs < 0 {
			t.Fatalf("expected non-negative queue delta, got %d", payload.QueueDeltaMs)
		}
		if payload.MatchID == "" {
			t.Fatalf("expected match id to be populated")
		}
	default:
		t.Fatalf("expected match_found message for alice")
	}
}

func TestMatchQueueTieBreaksBySequence(t *testing.T) {
	queue := newMatchQueue(4)

	now := time.Now()

	c1 := &Client{id: "alice"}
	c2 := &Client{id: "bob"}

	first := &queueItem{client: c1, joinedAt: now, sequence: 1}
	second := &queueItem{client: c2, joinedAt: now, sequence: 2}

	idxFirst := queue.insert(first)
	idxSecond := queue.insert(second)

	if idxFirst != 0 {
		t.Fatalf("expected first insert index 0, got %d", idxFirst)
	}
	if idxSecond != 1 {
		t.Fatalf("expected second insert index 1, got %d", idxSecond)
	}

	items := queue.items()
	if len(items) != 2 {
		t.Fatalf("expected two items, got %d", len(items))
	}
	if items[0].client != c1 {
		t.Fatalf("expected alice first in queue")
	}
	if items[1].client != c2 {
		t.Fatalf("expected bob second in queue")
	}
}
