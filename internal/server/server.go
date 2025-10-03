package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/valkey-io/valkey-go"
)

var jsonPool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 5120
	// Rate limit: max 10 messages per second per client
	rateLimitInterval = time.Second / 10
)

const (
	defaultValkeyQueueKey   = "playsock:queue"
	defaultValkeySessionKey = "playsock:session"
	defaultValkeyOpTimeout  = 2 * time.Second
)

// Action names the server understands.
const (
	ActionJoinQueue    = "join_queue"
	ActionQueued       = "queued"
	ActionMatchFound   = "match_found"
	ActionSubmitAnswer = "submit_answer"
	ActionRelayAnswer  = "opponent_answer"
	ActionScoreUpdate  = "score_update"
	ActionGameOver     = "game_over"
	ActionOpponentLeft = "opponent_left"
	ActionError        = "error"
)

// Envelope wraps client/server messages in a consistent format.
type Envelope struct {
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data"`
}

// joinQueuePayload describes the payload the client sends when requesting matchmaking.
type joinQueuePayload struct {
	PlayerID    string   `json:"player_id"`
	DisplayName string   `json:"display_name"`
	BetAmount   *float64 `json:"bet_amount,omitempty"`
	BetToken    string   `json:"bet_token,omitempty"`
}

// queuedPayload is sent back to the client after joining the queue.
type queuedPayload struct {
	Position int `json:"position"`
}

// matchFoundPayload informs the client that a match has been created.
type matchFoundPayload struct {
	MatchID        string   `json:"match_id"`
	OpponentID     string   `json:"opponent_id"`
	OpponentName   string   `json:"opponent_name"`
	BetAmount      *float64 `json:"bet_amount,omitempty"`
	BetToken       string   `json:"bet_token,omitempty"`
	YouArePlayerID string   `json:"player_id"`
	QueueDeltaMs   int64    `json:"queue_delta_ms"`
}

// submitAnswerPayload is received when a client submits an answer.
type submitAnswerPayload struct {
	MatchID     string   `json:"match_id"`
	PlayerID    string   `json:"player_id"`
	QuestionID  string   `json:"question_id"`
	Answer      string   `json:"answer"`
	Correct     bool     `json:"correct"`
	ScoreDelta  int      `json:"score_delta"`
	Final       bool     `json:"final"`
	RoundNumber int      `json:"round_number"`
	Notes       string   `json:"notes,omitempty"`
	BetAmount   *float64 `json:"bet_amount,omitempty"`
	BetToken    string   `json:"bet_token,omitempty"`
}

// scoreUpdatePayload notifies both players of score changes.
type scoreUpdatePayload struct {
	MatchID  string         `json:"match_id"`
	Scores   map[string]int `json:"scores"`
	Updated  string         `json:"updated_player_id"`
	Correct  bool           `json:"correct"`
	Question string         `json:"question_id"`
	Round    int            `json:"round_number"`
}

type opponentAnswerPayload struct {
	MatchID    string `json:"match_id"`
	PlayerID   string `json:"player_id"`
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
	Correct    bool   `json:"correct"`
	Round      int    `json:"round_number"`
}

// gameOverPayload is sent at the end of a match.
type gameOverPayload struct {
	MatchID   string         `json:"match_id"`
	WinnerID  string         `json:"winner_id"`
	Reason    string         `json:"reason"`
	Scores    map[string]int `json:"scores"`
	BetAmount *float64       `json:"bet_amount,omitempty"`
	BetToken  string         `json:"bet_token,omitempty"`
}

// opponentLeftPayload lets the remaining player know their opponent disconnected.
type opponentLeftPayload struct {
	MatchID    string `json:"match_id"`
	OpponentID string `json:"opponent_id"`
}

// errorPayload is sent when the server needs to report an issue to a client.
type errorPayload struct {
	Message string `json:"message"`
}

// Config controls runtime behaviour for the WebSocket server.
type Config struct {
	// AllowedOrigins optionally lists origins to accept; leave empty to allow all.
	AllowedOrigins []string
	// QueueTimeout optionally bounds how long a player can idle in the queue.
	QueueTimeout time.Duration
	// HandshakeTimeout controls how long an upgrade handshake may take before being aborted.
	HandshakeTimeout time.Duration
	// MaxConnectionsPerIP limits concurrent connections per remote IP when > 0.
	MaxConnectionsPerIP int
	// AuthTokens enumerates bearer tokens permitted to connect; empty slice disables token checks.
	AuthTokens []string
	// Valkey holds connection information for coordinating state across instances.
	Valkey *ValkeyConfig
	// Redis is deprecated; kept for backward compatibility with older env vars.
	// If both Valkey and Redis are provided, Valkey takes precedence.
	Redis *ValkeyConfig // deprecated legacy name
}

// ValkeyConfig captures the connection parameters for Valkey integration.
type ValkeyConfig struct {
	Addr             string
	Username         string
	Password         string
	DB               int
	QueueKey         string
	SessionKeyPrefix string
	OperationTimeout time.Duration
}

type valkeyStore interface {
	queueAdd(entry valkeyQueueEntry) error
	queueRemove(playerID string) error
	sessionSave(record valkeySessionRecord) error
	sessionDelete(matchID string) error
}

// Server hosts the matchmaking and game coordination logic over WebSockets.
type Server struct {
	cfg               Config
	lobby             *Lobby
	upgrader          websocket.Upgrader
	authTokens        map[string]struct{}
	connectionLimiter *connectionLimiter
}

// New constructs a Server with sensible defaults.
func New(cfg Config) *Server {
	var store valkeyStore
	// Prefer explicit Valkey config. Fall back to deprecated Redis field if present.
	vc := cfg.Valkey
	if vc == nil && cfg.Redis != nil {
		vc = cfg.Redis
	}
	if vc != nil && vc.Addr != "" {
		adapter, err := newValkeyAdapter(*vc)
		if err != nil {
			log.Printf("valkey integration disabled: %v", err)
		} else {
			store = adapter
		}
	}

	lobby := NewLobby(cfg.QueueTimeout, store)
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if len(cfg.AllowedOrigins) == 0 {
				return true
			}
			// Check for wildcard
			for _, allowed := range cfg.AllowedOrigins {
				if allowed == "*" {
					return true
				}
			}
			origin := r.Header.Get("Origin")
			for _, allowed := range cfg.AllowedOrigins {
				if origin == allowed {
					return true
				}
			}
			return false
		},
	}
	handshakeTimeout := cfg.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 5 * time.Second
	}
	upgrader.HandshakeTimeout = handshakeTimeout

	limiter := newConnectionLimiter(cfg.MaxConnectionsPerIP)
	tokenSet := make(map[string]struct{}, len(cfg.AuthTokens))
	for _, token := range cfg.AuthTokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		tokenSet[token] = struct{}{}
	}
	if len(tokenSet) == 0 {
		tokenSet = nil
	}

	return &Server{
		cfg:               cfg,
		lobby:             lobby,
		upgrader:          upgrader,
		authTokens:        tokenSet,
		connectionLimiter: limiter,
	}
}

// HandleWS upgrades an HTTP request to a WebSocket and starts handling the player lifecycle.
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	peerIP := remoteAddr(r)
	if len(s.authTokens) > 0 {
		token := extractBearer(r.Header.Get("Authorization"))
		if token == "" {
			log.Printf("unauthorized websocket attempt from %s: missing bearer token", peerIP)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, ok := s.authTokens[token]; !ok {
			log.Printf("unauthorized websocket attempt from %s: invalid token", peerIP)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var release func()
	if s.connectionLimiter != nil {
		rel, ok := s.connectionLimiter.acquire(peerIP)
		if !ok {
			log.Printf("rejecting websocket from %s: connection limit reached", peerIP)
			http.Error(w, "too many connections", http.StatusTooManyRequests)
			return
		}
		release = rel
		defer func() {
			if release != nil {
				release()
			}
		}()
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	client := newClient(conn, s.lobby, peerIP, release)
	release = nil
	log.Printf("websocket connection established from %s", peerIP)
	client.start()
}

// Lobby orchestrates matchmaking and active sessions.
type Lobby struct {
	mu           sync.Mutex
	queue        *matchQueue
	sessions     map[string]*Session
	queueTimeout time.Duration
	store        valkeyStore
	sequence     uint64
}

// NewLobby creates a lobby ready to accept players.
func NewLobby(queueTimeout time.Duration, store valkeyStore) *Lobby {
	return &Lobby{
		queue:        newMatchQueue(64),
		sessions:     make(map[string]*Session),
		queueTimeout: queueTimeout,
		store:        store,
	}
}

// Client represents a connected player.
type Client struct {
	lobby       *Lobby
	conn        *websocket.Conn
	send        chan []byte
	closeOnce   sync.Once
	closed      chan struct{}
	peerIP      string
	releaseConn func()

	id        string
	name      string
	betAmount *float64
	betToken  string
	matchID   string
	joinedAt  time.Time
	lastMsgAt time.Time
}

func newClient(conn *websocket.Conn, lobby *Lobby, peerIP string, release func()) *Client {
	return &Client{
		lobby:       lobby,
		conn:        conn,
		send:        make(chan []byte, 8),
		closed:      make(chan struct{}),
		peerIP:      peerIP,
		releaseConn: release,
		joinedAt:    time.Now(),
	}
}

func (c *Client) start() {
	go c.writePump()
	go c.readPump()
}

func (c *Client) readPump() {
	defer c.close("client closed connection")

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("unexpected close error: %v", err)
			}
			return
		}

		// Rate limiting: check if message is too frequent
		now := time.Now()
		if now.Sub(c.lastMsgAt) < rateLimitInterval {
			c.sendError("rate limit exceeded")
			continue
		}
		c.lastMsgAt = now

		var envelope Envelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			c.sendError("invalid message format")
			continue
		}

		switch envelope.Action {
		case ActionJoinQueue:
			c.handleJoinQueue(envelope.Data)
		case ActionSubmitAnswer:
			c.handleSubmitAnswer(envelope.Data)
		default:
			c.sendError("unsupported action")
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.close("writePump exit")
	}()

	for {
		select {
		case message, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Client) sendEnvelope(action string, payload any) {
	buf := jsonPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		jsonPool.Put(buf)
	}()

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("failed to marshal payload for %s: %v", action, err)
		return
	}
	env := Envelope{Action: action, Data: body}
	envBytes, err := json.Marshal(env)
	if err != nil {
		log.Printf("failed to wrap payload for %s: %v", action, err)
		return
	}
	select {
	case c.send <- envBytes:
	case <-time.After(2 * time.Second):
		log.Printf("dropping message to %s due to slow consumer", c.id)
	}
}

func (c *Client) sendError(message string) {
	c.sendEnvelope(ActionError, errorPayload{Message: message})
}

func extractBearer(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func remoteAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (c *Client) handleJoinQueue(raw json.RawMessage) {
	var payload joinQueuePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		c.sendError("invalid join_queue payload")
		return
	}
	if payload.PlayerID == "" {
		c.sendError("player_id is required")
		return
	}
	if c.id != "" && c.id != payload.PlayerID {
		c.sendError("player already registered")
		return
	}

	c.id = payload.PlayerID
	c.name = payload.DisplayName
	c.betAmount = payload.BetAmount
	c.betToken = payload.BetToken

	position, session := c.lobby.enqueue(c)
	c.sendEnvelope(ActionQueued, queuedPayload{Position: position})

	if session != nil {
		c.lobby.notifyMatchFound(session)
	}
}

func (c *Client) handleSubmitAnswer(raw json.RawMessage) {
	var payload submitAnswerPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		c.sendError("invalid submit_answer payload")
		return
	}
	if payload.MatchID == "" || payload.PlayerID == "" {
		c.sendError("match_id and player_id are required")
		return
	}
	if payload.PlayerID != c.id {
		c.sendError("player mismatch")
		return
	}
	if payload.MatchID != c.matchID {
		c.sendError("invalid match context")
		return
	}

	if err := c.lobby.handleAnswer(payload); err != nil {
		c.sendError(err.Error())
	}
}

func (c *Client) close(reason string) {
	c.closeOnce.Do(func() {
		if reason != "" {
			log.Printf("closing websocket for %s (%s): %s", c.id, c.peerIP, reason)
		}
		close(c.closed)
		c.lobby.handleDisconnect(c, reason)
		close(c.send)
		_ = c.conn.Close()
		if c.releaseConn != nil {
			c.releaseConn()
			c.releaseConn = nil
		}
	})
}

// Session encapsulates an active match between two players.
type Session struct {
	ID        string
	players   map[string]*Client
	scores    map[string]int
	betAmount *float64
	betToken  string
	created   time.Time
	queueGap  time.Duration
	pairedAt  time.Time
}

func newSession(c1, c2 *Client) *Session {
	id := uuid.NewString()
	betAmount := mergeBetAmount(c1.betAmount, c2.betAmount)
	betToken := mergeBetToken(c1.betToken, c2.betToken)

	session := &Session{
		ID:        id,
		players:   map[string]*Client{c1.id: c1, c2.id: c2},
		scores:    map[string]int{c1.id: 0, c2.id: 0},
		betAmount: betAmount,
		betToken:  betToken,
		created:   time.Now(),
		pairedAt:  time.Now(),
	}
	c1.matchID = id
	c2.matchID = id
	return session
}

// enqueue stores the client in the queue. If a pair is formed, the session is returned.
func (l *Lobby) enqueue(c *Client) (position int, session *Session) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	if l.store != nil {
		if err := l.store.queueRemove(c.id); err != nil {
			log.Printf("valkey queueRemove failed for %s: %v", c.id, err)
		}
	}

	if removed := l.queue.removeByID(c.id); removed != nil {
		removed.client.matchID = ""
	}

	c.joinedAt = now
	item := &queueItem{
		client:   c,
		joinedAt: now,
		sequence: atomic.AddUint64(&l.sequence, 1),
	}

	positionIdx := l.queue.insert(item)

	session, first, second := l.tryMatchLocked()
	if session != nil {
		gap := second.joinedAt.Sub(first.joinedAt)
		if gap < 0 {
			gap = -gap
		}
		session.queueGap = gap
		session.pairedAt = time.Now()
		l.sessions[session.ID] = session

		if l.store != nil {
			for _, id := range []string{first.client.id, second.client.id} {
				if err := l.store.queueRemove(id); err != nil {
					log.Printf("valkey queueRemove failed for %s: %v", id, err)
				}
			}
			if err := l.store.sessionSave(sessionRecordFromSession(session)); err != nil {
				log.Printf("valkey sessionSave failed for %s: %v", session.ID, err)
			}
		}

		log.Printf("paired %s vs %s (Δ %dms)", first.client.id, second.client.id, gap.Milliseconds())
		return 0, session
	}

	if l.store != nil {
		if err := l.store.queueAdd(queueEntryFromClient(c)); err != nil {
			log.Printf("valkey queueAdd failed for %s: %v", c.id, err)
		}
	}
	return positionIdx + 1, nil
}

func (l *Lobby) notifyMatchFound(s *Session) {
	for playerID, client := range s.players {
		var opponentID, opponentName string
		for id, other := range s.players {
			if id != playerID {
				opponentID = id
				opponentName = other.name
				break
			}
		}
		client.sendEnvelope(ActionMatchFound, matchFoundPayload{
			MatchID:        s.ID,
			OpponentID:     opponentID,
			OpponentName:   opponentName,
			BetAmount:      s.betAmount,
			BetToken:       s.betToken,
			YouArePlayerID: playerID,
			QueueDeltaMs:   s.queueGap.Milliseconds(),
		})
	}
}

func (l *Lobby) handleAnswer(payload submitAnswerPayload) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	session, ok := l.sessions[payload.MatchID]
	if !ok {
		return errors.New("unknown match_id")
	}
	if _, exists := session.players[payload.PlayerID]; !exists {
		return errors.New("player not part of this match")
	}

	// Update scores.
	if payload.ScoreDelta != 0 {
		session.scores[payload.PlayerID] += payload.ScoreDelta
	} else if payload.Correct {
		session.scores[payload.PlayerID]++
	}

	update := scoreUpdatePayload{
		MatchID:  session.ID,
		Scores:   copyScores(session.scores),
		Updated:  payload.PlayerID,
		Correct:  payload.Correct,
		Question: payload.QuestionID,
		Round:    payload.RoundNumber,
	}

	relay := opponentAnswerPayload{
		MatchID:    session.ID,
		PlayerID:   payload.PlayerID,
		QuestionID: payload.QuestionID,
		Answer:     payload.Answer,
		Correct:    payload.Correct,
		Round:      payload.RoundNumber,
	}

	for id, client := range session.players {
		client.sendEnvelope(ActionScoreUpdate, update)
		if id != payload.PlayerID {
			client.sendEnvelope(ActionRelayAnswer, relay)
		}
	}

	if payload.Final {
		l.finishMatchLocked(session, "final_round")
	} else if l.store != nil {
		if err := l.store.sessionSave(sessionRecordFromSession(session)); err != nil {
			log.Printf("valkey sessionSave failed for %s: %v", session.ID, err)
		}
	}

	return nil
}

func (l *Lobby) handleDisconnect(c *Client, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Remove from queue if present.
	if l.queue.removeByID(c.id) != nil {
		if l.store != nil {
			if err := l.store.queueRemove(c.id); err != nil {
				log.Printf("valkey queueRemove failed for %s: %v", c.id, err)
			}
		}
	}

	// Handle active session.
	if c.matchID == "" {
		return
	}

	session, ok := l.sessions[c.matchID]
	if !ok {
		return
	}

	opponent := session.otherPlayer(c.id)
	c.matchID = ""
	delete(session.players, c.id)
	delete(session.scores, c.id)
	delete(l.sessions, session.ID)

	if l.store != nil {
		if err := l.store.sessionDelete(session.ID); err != nil {
			log.Printf("valkey sessionDelete failed for %s: %v", session.ID, err)
		}
	}

	if opponent != nil {
		opponent.matchID = ""
		opponent.sendEnvelope(ActionOpponentLeft, opponentLeftPayload{
			MatchID:    session.ID,
			OpponentID: c.id,
		})
		// Remaining player wins by default.
		gameOver := gameOverPayload{
			MatchID:  session.ID,
			WinnerID: opponent.id,
			Reason:   reason,
			Scores: map[string]int{
				opponent.id: session.scores[opponent.id],
			},
			BetAmount: session.betAmount,
			BetToken:  session.betToken,
		}
		opponent.sendEnvelope(ActionGameOver, gameOver)
	}
}

func (l *Lobby) finishMatchLocked(session *Session, reason string) {
	delete(l.sessions, session.ID)

	if l.store != nil {
		if err := l.store.sessionDelete(session.ID); err != nil {
			log.Printf("valkey sessionDelete failed for %s: %v", session.ID, err)
		}
	}

	var winnerID string
	topScore := -1
	tie := false
	for id, score := range session.scores {
		if score > topScore {
			topScore = score
			winnerID = id
			tie = false
		} else if score == topScore {
			tie = true
		}
	}

	if tie {
		winnerID = ""
	}

	payload := gameOverPayload{
		MatchID:   session.ID,
		WinnerID:  winnerID,
		Reason:    reason,
		Scores:    copyScores(session.scores),
		BetAmount: session.betAmount,
		BetToken:  session.betToken,
	}

	for _, client := range session.players {
		client.matchID = ""
		client.sendEnvelope(ActionGameOver, payload)
	}
}

func (l *Lobby) tryMatchLocked() (*Session, *queueItem, *queueItem) {
	entries := l.queue.items()
	for i := 0; i < len(entries); i++ {
		first := entries[i]
		if first == nil {
			continue
		}
		for j := i + 1; j < len(entries); j++ {
			second := entries[j]
			if second == nil {
				continue
			}
			if first.client.id == second.client.id {
				continue
			}
			if compatibleBets(first.client.betAmount, second.client.betAmount, first.client.betToken, second.client.betToken) {
				l.queue.removeByID(first.client.id)
				l.queue.removeByID(second.client.id)
				session := newSession(first.client, second.client)
				return session, first, second
			}
		}
	}
	return nil, nil, nil
}

func (s *Session) otherPlayer(id string) *Client {
	for pid, client := range s.players {
		if pid != id {
			return client
		}
	}
	return nil
}

type valkeyQueueEntry struct {
	PlayerID    string    `json:"player_id"`
	DisplayName string    `json:"display_name"`
	BetAmount   *float64  `json:"bet_amount,omitempty"`
	BetToken    string    `json:"bet_token,omitempty"`
	JoinedAt    time.Time `json:"joined_at"`
}
type valkeySessionRecord struct {
	MatchID   string         `json:"match_id"`
	PlayerIDs []string       `json:"player_ids"`
	Scores    map[string]int `json:"scores"`
	BetAmount *float64       `json:"bet_amount,omitempty"`
	BetToken  string         `json:"bet_token,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
}

func queueEntryFromClient(c *Client) valkeyQueueEntry {
	return valkeyQueueEntry{
		PlayerID:    c.id,
		DisplayName: c.name,
		BetAmount:   cloneFloatPointer(c.betAmount),
		BetToken:    c.betToken,
		JoinedAt:    c.joinedAt,
	}
}

func sessionRecordFromSession(s *Session) valkeySessionRecord {
	playerIDs := make([]string, 0, len(s.players))
	for id := range s.players {
		playerIDs = append(playerIDs, id)
	}
	return valkeySessionRecord{
		MatchID:   s.ID,
		PlayerIDs: playerIDs,
		Scores:    copyScores(s.scores),
		BetAmount: cloneFloatPointer(s.betAmount),
		BetToken:  s.betToken,
		UpdatedAt: time.Now(),
	}
}

func cloneFloatPointer(val *float64) *float64 {
	if val == nil {
		return nil
	}
	copy := *val
	return &copy
}

func compatibleBets(a, b *float64, tokenA, tokenB string) bool {
	if tokenA != "" && tokenB != "" && tokenA != tokenB {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return true
	}
	return absFloat(*a-*b) < 1e-9
}

func mergeBetAmount(a, b *float64) *float64 {
	if a != nil && b != nil {
		avg := (*a + *b) / 2
		return &avg
	}
	if a != nil {
		val := *a
		return &val
	}
	if b != nil {
		val := *b
		return &val
	}
	return nil
}

func mergeBetToken(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func absFloat(val float64) float64 {
	if val < 0 {
		return -val
	}
	return val
}

func copyScores(scores map[string]int) map[string]int {
	clone := make(map[string]int, len(scores))
	for k, v := range scores {
		clone[k] = v
	}
	return clone
}

type valkeyAdapter struct {
	client        valkey.Client
	queueKey      string
	sessionPrefix string
	timeout       time.Duration
}

func newValkeyAdapter(cfg ValkeyConfig) (*valkeyAdapter, error) {
	timeout := cfg.OperationTimeout
	if timeout <= 0 {
		timeout = defaultValkeyOpTimeout
	}

	queueKey := cfg.QueueKey
	if queueKey == "" {
		queueKey = defaultValkeyQueueKey
	}

	sessionPrefix := cfg.SessionKeyPrefix
	if sessionPrefix == "" {
		sessionPrefix = defaultValkeySessionKey
	}

	clientOpt := valkey.ClientOption{
		InitAddress: []string{cfg.Addr},
	}
	if cfg.Username != "" {
		clientOpt.Username = cfg.Username
	}
	if cfg.Password != "" {
		clientOpt.Password = cfg.Password
	}
	if cfg.DB != 0 {
		clientOpt.SelectDB = cfg.DB
	}

	client, err := valkey.NewClient(clientOpt)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Do(ctx, client.B().Ping().Build()).Error(); err != nil {
		client.Close()
		return nil, err
	}

	return &valkeyAdapter{
		client:        client,
		queueKey:      queueKey,
		sessionPrefix: sessionPrefix,
		timeout:       timeout,
	}, nil
}

func (r *valkeyAdapter) queueAdd(entry valkeyQueueEntry) error {
	if entry.PlayerID == "" {
		return nil
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Do(ctx, r.client.B().Hset().Key(r.queueKey).FieldValue().FieldValue(entry.PlayerID, string(payload)).Build()).Error()
}

func (r *valkeyAdapter) queueRemove(playerID string) error {
	if playerID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Do(ctx, r.client.B().Hdel().Key(r.queueKey).Field(playerID).Build()).Error()
}

func (r *valkeyAdapter) sessionSave(record valkeySessionRecord) error {
	if record.MatchID == "" {
		return nil
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Do(ctx, r.client.B().Set().Key(r.sessionKey(record.MatchID)).Value(string(payload)).Build()).Error()
}

func (r *valkeyAdapter) sessionDelete(matchID string) error {
	if matchID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return r.client.Do(ctx, r.client.B().Del().Key(r.sessionKey(matchID)).Build()).Error()
}

func (r *valkeyAdapter) sessionKey(matchID string) string {
	return fmt.Sprintf("%s:%s", r.sessionPrefix, matchID)
}

func (r *valkeyAdapter) Close() {
	r.client.Close()
}
