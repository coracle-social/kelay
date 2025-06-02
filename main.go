package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip44"
	"github.com/nbd-wtf/go-nostr/nip59"
)

type Session struct {
	conn     *websocket.Conn
	lastSeen time.Time
	mu       sync.Mutex
	pubkey   string
	messages chan string
	ctx      context.Context
	cancel   context.CancelFunc
}

var (
	sessions     = make(map[string]*Session)
	connMu       sync.RWMutex
	kelayPrivKey string
	kelayPubKey  string
)

func main() {
	// Read environment variables
	brokerRelaysStr := os.Getenv("BROKER_RELAYS")
	if brokerRelaysStr == "" {
		log.Fatal("BROKER_RELAYS environment variable is required")
	}
	brokerRelays := strings.Split(brokerRelaysStr, ",")

	relayBackend := os.Getenv("RELAY_BACKEND")
	if relayBackend == "" {
		log.Fatal("RELAY_BACKEND environment variable is required")
	}

	kelayPrivKey = os.Getenv("KELAY_SECRET")
	if kelayPrivKey == "" {
		log.Fatal("KELAY_SECRET environment variable is required")
	}

	// Derive public key from private key
	pk, err := nostr.GetPublicKey(kelayPrivKey)
	if err != nil {
		log.Fatalf("Failed to derive public key: %v", err)
	}
	kelayPubKey = pk

	log.Printf("Kelay public key: %s", kelayPubKey)

	// Connect to broker relays
	ctx := context.Background()
	var brokerConns []*nostr.Relay

	for _, url := range brokerRelays {
		relay, err := nostr.RelayConnect(ctx, strings.TrimSpace(url))
		if err != nil {
			log.Printf("Failed to connect to broker relay %s: %v", url, err)
			continue
		}
		brokerConns = append(brokerConns, relay)
		log.Printf("Connected to broker relay: %s", url)
	}

	if len(brokerConns) == 0 {
		log.Fatal("Failed to connect to any broker relays")
	}

	// Subscribe to kind 21059 events
	filters := []nostr.Filter{{
		Kinds: []int{21059},
		Tags: nostr.TagMap{
			"p": []string{kelayPubKey},
		},
	}}

	for _, relay := range brokerConns {
		sub, err := relay.Subscribe(ctx, filters)
		if err != nil {
			log.Printf("Failed to subscribe to relay %s: %v", relay.URL, err)
			continue
		}

		go handleBrokerEvents(ctx, sub, relayBackend, brokerConns)
	}

	// Start connection cleanup routine
	go cleanupConnections()

	// Keep the program running
	select {}
}

func handleBrokerEvents(ctx context.Context, sub *nostr.Subscription, relayBackend string, brokerConns []*nostr.Relay) {
	for ev := range sub.Events {
		go processEnvelope(ctx, ev, relayBackend, brokerConns)
	}
}

func processEnvelope(ctx context.Context, envelope *nostr.Event, relayBackend string, brokerConns []*nostr.Relay) {
	// Unwrap the kind 21059 envelope
	innerEvent, err := nip59.GiftUnwrap(*envelope, decrypt)
	if err != nil {
		log.Printf("Failed to unwrap envelope: %v", err)
		return
	}

	// Check if inner event is kind 1507
	if innerEvent.Kind != 1507 {
		log.Printf("Inner event is not kind 1507, got kind %d", innerEvent.Kind)
		return
	}

	pubkey := innerEvent.PubKey

	// Get or create connection for this user
	session := getOrCreateSession(ctx, pubkey, relayBackend, brokerConns)
	if session == nil {
		return
	}

	log.Printf("Forwarding message from %s: %s\n", pubkey, innerEvent.Content)

	// Forward the message to backend relay
	session.mu.Lock()
	err = session.conn.WriteMessage(websocket.TextMessage, []byte(innerEvent.Content))
	session.mu.Unlock()
	if err != nil {
		log.Printf("Failed to write to backend relay: %v", err)
		return
	}
}

func getOrCreateSession(ctx context.Context, pubkey, relayBackend string, brokerConns []*nostr.Relay) *Session {
	connMu.Lock()
	defer connMu.Unlock()

	if session, exists := sessions[pubkey]; exists {
		session.mu.Lock()
		session.lastSeen = time.Now()
		session.mu.Unlock()
		return session
	}

	// Create new WebSocket connection
	dialer := websocket.DefaultDialer
	conn, _, err := dialer.Dial(relayBackend, nil)
	if err != nil {
		log.Printf("Failed to connect to backend relay for user %s: %v", pubkey, err)
		return nil
	}

	// Create a context for this connection
	connCtx, cancel := context.WithCancel(ctx)

	session := &Session{
		conn:     conn,
		lastSeen: time.Now(),
		pubkey:   pubkey,
		messages: make(chan string, 100),
		ctx:      connCtx,
		cancel:   cancel,
	}
	sessions[pubkey] = session

	// Start goroutine to read messages from WebSocket
	go func() {
		defer func() {
			cancel()
			conn.Close()
			connMu.Lock()
			delete(sessions, pubkey)
			connMu.Unlock()
		}()

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket error for user %s: %v", pubkey, err)
				} else {
					log.Println(err)
				}
				return
			}

			select {
			case session.messages <- string(message):
			case <-connCtx.Done():
				return
			}
		}
	}()

	// Start handler for backend responses
	go handleBackendResponses(connCtx, session, brokerConns)

	return session
}

func handleBackendResponses(ctx context.Context, session *Session, brokerConns []*nostr.Relay) {
	for {
		select {
		case message := <-session.messages:
			// Update last seen time
			session.mu.Lock()
			session.lastSeen = time.Now()
			session.mu.Unlock()

			// Create kind 1508 event
			rumor := nostr.Event{
				Kind:      1508,
				CreatedAt: nostr.Now(),
				Content:   message,
			}

			sign(&rumor)

			// Encrypt function for gift wrap
			thisEncrypt := func(plaintext string) (string, error) {
				return encrypt(session.pubkey, plaintext)
			}

			// Modify function for gift wrap
			modify := func(event *nostr.Event) {
				event.Kind = 21059
				event.CreatedAt = nostr.Now()
			}

			// Wrap in kind 21059
			wrapped, err := nip59.GiftWrap(rumor, session.pubkey, thisEncrypt, sign, modify)
			if err != nil {
				log.Printf("Failed to wrap response: %v", err)
				continue
			}

			// Send to all broker relays
			for _, relay := range brokerConns {
				if err := relay.Publish(ctx, wrapped); err != nil {
					log.Printf("Failed to publish to broker relay %s: %v", relay.URL, err)
				}
			}

		case <-ctx.Done():
			return
		}
	}
}

func sign(event *nostr.Event) error {
	return event.Sign(kelayPrivKey)
}

func encrypt(otherpubkey, plaintext string) (string, error) {
	conversationKey, err := nip44.GenerateConversationKey(otherpubkey, kelayPrivKey)
	if err != nil {
		return "", err
	}
	return nip44.Encrypt(plaintext, conversationKey)
}

func decrypt(otherpubkey, ciphertext string) (string, error) {
	conversationKey, err := nip44.GenerateConversationKey(otherpubkey, kelayPrivKey)
	if err != nil {
		return "", err
	}
	return nip44.Decrypt(ciphertext, conversationKey)
}

func cleanupConnections() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		connMu.Lock()
		now := time.Now()

		for pubkey, conn := range sessions {
			conn.mu.Lock()
			if now.Sub(conn.lastSeen) > 30*time.Second {
				conn.cancel()
				conn.conn.Close()
				delete(sessions, pubkey)
				log.Printf("Closed inactive connection for user %s", pubkey)
			}
			conn.mu.Unlock()
		}

		connMu.Unlock()
	}
}
