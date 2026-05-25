package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"nhooyr.io/websocket"

	"vulpineos/internal/juggler"
)

// Client connects to a remote VulpineOS server via WebSocket.
// It implements the juggler.Transport interface so the TUI can use it
// identically to a local pipe connection.
type Client struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	recvCh  chan *juggler.Message
	writeMu sync.Mutex

	controlMu      sync.Mutex
	nextControlID  int
	controlPending map[int]chan controlResponse
}

type controlResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// Dial connects to a remote VulpineOS server.
func Dial(ctx context.Context, url string, apiKey string) (*Client, error) {
	headers := http.Header{}
	subprotocols := []string(nil)
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
		if protocol := AccessSubprotocol(apiKey); protocol != "" {
			subprotocols = []string{protocol}
		}
	}

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader:   headers,
		Subprotocols: subprotocols,
	})
	if err != nil {
		return nil, fmt.Errorf("dial remote: %w", err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	c := &Client{
		conn:           conn,
		ctx:            childCtx,
		cancel:         cancel,
		recvCh:         make(chan *juggler.Message, 64),
		controlPending: make(map[int]chan controlResponse),
	}

	go c.readLoop()
	return c, nil
}

// Send sends a Juggler message to the remote server.
func (c *Client) Send(msg *juggler.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	env, err := NewJugglerEnvelope(data)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(c.ctx, websocket.MessageText, env)
}

// ControlCall sends a TUI control command over the remote websocket.
func (c *Client) ControlCall(ctx context.Context, method string, params any, result any) error {
	id, ch := c.registerControlCall()
	payload, err := json.Marshal(map[string]any{
		"command": method,
		"params":  params,
		"id":      id,
	})
	if err != nil {
		c.unregisterControlCall(id)
		return err
	}
	env, err := json.Marshal(Envelope{
		Type:    "control",
		Payload: json.RawMessage(payload),
	})
	if err != nil {
		c.unregisterControlCall(id)
		return err
	}

	c.writeMu.Lock()
	err = c.conn.Write(c.ctx, websocket.MessageText, env)
	c.writeMu.Unlock()
	if err != nil {
		c.unregisterControlCall(id)
		return err
	}

	select {
	case resp := <-ch:
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decode control result: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		c.unregisterControlCall(id)
		return ctx.Err()
	case <-c.ctx.Done():
		c.unregisterControlCall(id)
		return c.ctx.Err()
	}
}

func (c *Client) registerControlCall() (int, chan controlResponse) {
	ch := make(chan controlResponse, 1)
	c.controlMu.Lock()
	c.nextControlID++
	id := c.nextControlID
	c.controlPending[id] = ch
	c.controlMu.Unlock()
	return id, ch
}

func (c *Client) unregisterControlCall(id int) {
	c.controlMu.Lock()
	delete(c.controlPending, id)
	c.controlMu.Unlock()
}

// Receive reads the next Juggler message from the remote server.
func (c *Client) Receive() (*juggler.Message, error) {
	select {
	case msg := <-c.recvCh:
		if msg == nil {
			return nil, fmt.Errorf("connection closed")
		}
		return msg, nil
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}
}

// Close disconnects from the remote server.
func (c *Client) Close() error {
	c.cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) readLoop() {
	defer c.cancel()
	defer c.failPendingControlCalls(errors.New("remote websocket closed"))
	defer close(c.recvCh)

	for {
		_, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}

		var env Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}

		if env.Type == "control" {
			c.handleControlEnvelope(env.Payload)
			continue
		}

		if env.Type != "juggler" {
			continue
		}

		var msg juggler.Message
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			continue
		}

		if !c.enqueueReceivedMessage(&msg) {
			return
		}
	}
}

func (c *Client) failPendingControlCalls(err error) {
	c.controlMu.Lock()
	pending := c.controlPending
	c.controlPending = make(map[int]chan controlResponse)
	c.controlMu.Unlock()
	for id, ch := range pending {
		ch <- controlResponse{ID: id, Error: err.Error()}
	}
}

func (c *Client) enqueueReceivedMessage(msg *juggler.Message) bool {
	select {
	case c.recvCh <- msg:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *Client) handleControlEnvelope(payload json.RawMessage) {
	var outer struct {
		Params json.RawMessage `json:"params"`
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(payload, &outer); err != nil {
		return
	}

	respPayload := payload
	if len(outer.Params) > 0 {
		respPayload = outer.Params
	}
	var resp controlResponse
	if err := json.Unmarshal(respPayload, &resp); err != nil {
		return
	}
	if resp.ID == 0 {
		resp.ID = outer.ID
		resp.Result = outer.Result
		resp.Error = outer.Error
	}
	if resp.ID == 0 {
		return
	}

	c.controlMu.Lock()
	ch := c.controlPending[resp.ID]
	delete(c.controlPending, resp.ID)
	c.controlMu.Unlock()
	if ch == nil {
		return
	}
	ch <- resp
}
