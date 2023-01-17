package tm

import (
	"context"
	"time"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/pkg/errors"
	"github.com/tendermint/tendermint/rpc/jsonrpc/client"
	"github.com/tendermint/tendermint/rpc/jsonrpc/types"
)

const (
	TM_WS_ENDPOINT    = "/websocket"
	TM_WS_PING_PERIOD = 10 * time.Second
	TM_WS_MAX_RECONNECT = 3
)

type Client struct {
	c *client.WSClient
}

type evenHandlerFunc func(context.Context, []byte) error

func NewClient(addr string) (*Client, error) {
	wsc, err := client.NewWS(addr, TM_WS_ENDPOINT,
		client.PingPeriod(TM_WS_PING_PERIOD),
		client.MaxReconnectAttempts(TM_WS_MAX_RECONNECT))
	if err != nil {
		return nil, errors.Wrap(err, "error creating ws client")
	}
	err = wsc.Start()
	if err != nil {
		return nil, errors.Wrap(err, "error connecting to WS")
	}
	return &Client{
		c: wsc,
	}, nil
}

// processes events from tm
// returns error on failure
// returns error when event handler returns error
func (c Client) Subscribe(ctx context.Context, q string, h evenHandlerFunc) (<-chan struct{}, chan error) {
	e := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		err := c.c.Subscribe(ctx, q)
		if err != nil {
			e <- errors.Wrap(err, "error sending subscribe request")
			return
		}
		for {
			var event types.RPCResponse
			select {
			case event = <- c.c.ResponsesCh:
			case <- c.c.Quit():
				e <- errors.New("ws conn closed")
				return
			}
			if event.Error != nil {
				e <- errors.Wrap(event.Error, "error reading from ws")
				return
			} 

			jsonBuf, err := event.Result.MarshalJSON()
			if err != nil {
				e <- errors.Wrap(err, "error parsing result")
				return
			}
			// hack: TM sends empty event to begin with. skipping
			if string(jsonBuf) == "{}" {
				logger.FromContext(ctx).Info("received empty event. continuing...")
				continue
			}
			err = h(ctx, jsonBuf)
			if err != nil {
				logger.FromContext(ctx).Error(errors.WithMessage(err, "error from event handler"))
			}
		}
	}()
	return ctx.Done(), e
}
