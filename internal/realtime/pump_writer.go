package realtime

import (
	"context"
	"time"

	"github.com/coder/websocket"
)

// WritePump continuously forwards buffered messages to the websocket connection.
func (c *Client) WritePump(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.sendChan:
			if !ok {
				_ = c.conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Write(writeCtx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
