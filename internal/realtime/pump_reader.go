package realtime

import (
	"context"
	"encoding/json"
)

// ReadPump continuously reads incoming raw messages from client connection.
func (c *Client) ReadPump(ctx context.Context) {
	defer func() {
		c.hub.Unregister(c)
		c.Close()
	}()

	for {
		_, payload, err := c.conn.Read(ctx)
		if err != nil {
			break
		}

		var actionMsg ActionMessage
		if err := json.Unmarshal(payload, &actionMsg); err == nil && actionMsg.Action != "" {
			go c.hub.Dispatch(ctx, c, actionMsg)
		}
	}
}
