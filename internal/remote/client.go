package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"nhooyr.io/websocket"

	"vulpineos/internal/juggler"
)

// Client connects to a remote VulpineOS server via WebSocket.
// It implements the juggler.Transport interface so the TUI can use it
// identically to a local pipe connection.
type Client struct {
	conn   *websocket.Conn
	ctx    context.Context
	cancel context.CancelFunc
	recvCh chan *juggler.Message
}

// Dial connects to a remote VulpineOS server.
func Dial(ctx context.Context, url string, apiKey string) (*Client, error) {
	headers := http.Header{}
	if apiKey != "" {
		headers.Set("Authorization", "Bearer "+apiKey)
	}

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return nil, fmt.Errorf("dial remote: %w", err)
	}

	childCtx, cancel := context.WithCancel(ctx)
	c := &Client{
		conn:   conn,
		ctx:    childCtx,
		cancel: cancel,
		recvCh: make(chan *juggler.Message, 64),
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
	return c.conn.Write(c.ctx, websocket.MessageText, env)
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
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) readLoop() {
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

		if env.Type != "juggler" {
			continue
		}

		var msg juggler.Message
		if err := json.Unmarshal(env.Payload, &msg); err != nil {
			continue
		}

		c.recvCh <- &msg
	}
}
