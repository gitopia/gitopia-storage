package tm

import (
	"context"

	"github.com/gitopia/gitopia-ipfs-bridge/logger"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/rpc/jsonrpc/client"
)

const (
	TM_WS_ENDPOINT = "/websocket"
)

type Client struct {
	c *client.WSClient
}

type evenHandlerFunc func(context.Context, []byte) error

func NewTmClient() (*Client, error) {
	wsc, err := client.NewWS(viper.GetString("tm_addr"), TM_WS_ENDPOINT)
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
			event := <-c.c.ResponsesCh
			if event.Error != nil {
				e <- errors.Wrap(err, "error reading from ws")
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
