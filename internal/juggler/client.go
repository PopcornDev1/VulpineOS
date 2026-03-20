package juggler

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
)

// EventHandler is called when a Juggler event is received.
type EventHandler func(params json.RawMessage)

// Client is a high-level Juggler protocol client.
type Client struct {
	transport Transport
	nextID    atomic.Int64
	pending   map[int]chan *Message
	pendingMu sync.Mutex
	handlers  map[string][]EventHandler
	handlerMu sync.RWMutex
	done      chan struct{}
	closeOnce sync.Once
}

// NewClient creates a Juggler client using the given transport.
func NewClient(transport Transport) *Client {
	c := &Client{
		transport: transport,
		pending:   make(map[int]chan *Message),
		handlers:  make(map[string][]EventHandler),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// Call sends a synchronous RPC request and waits for the response.
func (c *Client) Call(sessionID, method string, params interface{}) (json.RawMessage, error) {
	id := int(c.nextID.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		var err error
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
	}

	msg := &Message{
		ID:        id,
		Method:    method,
		Params:    rawParams,
		SessionID: sessionID,
	}

	ch := make(chan *Message, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()

	if err := c.transport.Send(msg); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-c.done:
		return nil, fmt.Errorf("client closed")
	}
}

// Subscribe registers a handler for a Juggler event.
func (c *Client) Subscribe(event string, handler EventHandler) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[event] = append(c.handlers[event], handler)
}

// Close shuts down the client and transport.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
	})
	return c.transport.Close()
}

func (c *Client) readLoop() {
	for {
		msg, err := c.transport.Receive()
		if err != nil {
			select {
			case <-c.done:
				return
			default:
				// Transport error — close client
				c.Close()
				return
			}
		}

		if msg.IsResponse() {
			c.pendingMu.Lock()
			ch, ok := c.pending[msg.ID]
			if ok {
				delete(c.pending, msg.ID)
			}
			c.pendingMu.Unlock()
			if ok {
				ch <- msg
			}
		} else if msg.IsEvent() {
			c.handlerMu.RLock()
			handlers := c.handlers[msg.Method]
			c.handlerMu.RUnlock()
			for _, h := range handlers {
				h(msg.Params)
			}
		}
	}
}
