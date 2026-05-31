package juggler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// EventHandler is called when a Juggler event is received.
// sessionID identifies which page session the event belongs to (empty for browser events).
type EventHandler func(sessionID string, params json.RawMessage)

type eventSubscription struct {
	id      int64
	handler EventHandler
}

// Client is a high-level Juggler protocol client.
type Client struct {
	transport     Transport
	nextID        atomic.Int64
	nextHandlerID atomic.Int64
	pending       map[int]chan *Message
	pendingMu     sync.Mutex
	handlers      map[string][]eventSubscription
	handlerMu     sync.RWMutex
	events        chan *Message
	droppedEvents atomic.Uint64
	done          chan struct{}
	closeOnce     sync.Once
}

// NewClient creates a Juggler client using the given transport.
func NewClient(transport Transport) *Client {
	c := &Client{
		transport: transport,
		pending:   make(map[int]chan *Message),
		handlers:  make(map[string][]eventSubscription),
		events:    make(chan *Message, 1024),
		done:      make(chan struct{}),
	}
	go c.readLoop()
	go c.eventLoop()
	return c
}

// DefaultCallTimeout is the default timeout for Call().
const DefaultCallTimeout = 60 * time.Second

// Call sends a synchronous RPC request and waits for the response with a default 60-second timeout.
func (c *Client) Call(sessionID, method string, params interface{}) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCallTimeout)
	defer cancel()
	return c.CallWithContext(ctx, sessionID, method, params)
}

// CallWithContext sends a synchronous RPC request and waits for the response, respecting the given context.
func (c *Client) CallWithContext(ctx context.Context, sessionID, method string, params interface{}) (json.RawMessage, error) {
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

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- c.transport.Send(msg)
	}()

	select {
	case err := <-sendErr:
		if err != nil {
			c.deletePending(id)
			return nil, err
		}
	case <-ctx.Done():
		c.deletePending(id)
		_ = c.Close()
		return nil, fmt.Errorf("call %s: %w", method, ctx.Err())
	case <-c.done:
		c.deletePending(id)
		return nil, fmt.Errorf("client closed")
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.deletePending(id)
		return nil, fmt.Errorf("call %s: %w", method, ctx.Err())
	case <-c.done:
		c.deletePending(id)
		return nil, fmt.Errorf("client closed")
	}
}

func (c *Client) deletePending(id int) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

// Subscribe registers a handler for a Juggler event.
func (c *Client) Subscribe(event string, handler EventHandler) {
	c.SubscribeWithCancel(event, handler)
}

// SubscribeWithCancel registers a handler and returns a cancellation function.
func (c *Client) SubscribeWithCancel(event string, handler EventHandler) func() {
	id := c.nextHandlerID.Add(1)
	c.handlerMu.Lock()
	c.handlers[event] = append(c.handlers[event], eventSubscription{id: id, handler: handler})
	c.handlerMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			c.handlerMu.Lock()
			defer c.handlerMu.Unlock()
			subs := c.handlers[event]
			for i, sub := range subs {
				if sub.id == id {
					c.handlers[event] = append(subs[:i], subs[i+1:]...)
					if len(c.handlers[event]) == 0 {
						delete(c.handlers, event)
					}
					return
				}
			}
		})
	}
}

// DroppedEvents returns the number of events dropped because the dispatch queue was full.
func (c *Client) DroppedEvents() uint64 {
	return c.droppedEvents.Load()
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
			c.queueEvent(msg)
		}
	}
}

func (c *Client) queueEvent(msg *Message) {
	select {
	case c.events <- msg:
	case <-c.done:
	default:
		c.droppedEvents.Add(1)
	}
}

func (c *Client) eventLoop() {
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.events:
			c.dispatchEvent(msg)
		}
	}
}

func (c *Client) dispatchEvent(msg *Message) {
	c.handlerMu.RLock()
	subs := append([]eventSubscription(nil), c.handlers[msg.Method]...)
	c.handlerMu.RUnlock()
	for _, sub := range subs {
		sub.handler(msg.SessionID, msg.Params)
	}
}
