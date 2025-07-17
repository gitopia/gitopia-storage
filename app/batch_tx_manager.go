package app

import (
	"context"
	"sync"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go"
	"github.com/sirupsen/logrus"
)

type BatchTxManager struct {
	client       gitopia.Client
	txQueue      []sdk.Msg
	queueMutex   sync.Mutex
	ticker       *time.Ticker
	stopCh       chan struct{}
	batchTimeout time.Duration
}

func NewBatchTxManager(client gitopia.Client, batchTimeout time.Duration) *BatchTxManager {
	return &BatchTxManager{
		client:       client,
		txQueue:      make([]sdk.Msg, 0),
		ticker:       time.NewTicker(batchTimeout),
		stopCh:       make(chan struct{}),
		batchTimeout: batchTimeout,
	}
}

func (b *BatchTxManager) Start() {
	go b.processBatches()
}

func (b *BatchTxManager) Stop() {
	b.ticker.Stop()
	close(b.stopCh)
	// Process any remaining messages
	b.processBatch()
}

func (b *BatchTxManager) AddToBatch(ctx context.Context, msgs ...sdk.Msg) error {
	b.queueMutex.Lock()
	defer b.queueMutex.Unlock()

	b.txQueue = append(b.txQueue, msgs...)
	return nil
}

func (b *BatchTxManager) processBatches() {
	for {
		select {
		case <-b.ticker.C:
			b.processBatch()
		case <-b.stopCh:
			return
		}
	}
}

func (b *BatchTxManager) processBatch() {
	b.queueMutex.Lock()
	if len(b.txQueue) == 0 {
		b.queueMutex.Unlock()
		return
	}

	// Take a copy of the current batch and reset the queue
	batch := make([]sdk.Msg, len(b.txQueue))
	copy(batch, b.txQueue)
	b.txQueue = b.txQueue[:0]
	b.queueMutex.Unlock()

	// Process the batch
	if len(batch) > 0 {
		// Use background context since the original context might be canceled
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Send the batch transaction
		if err := b.client.BroadcastTxAndWait(ctx, batch...); err != nil {
			// Log the error using logrus
			logrus.WithError(err).Error("error broadcasting batch transaction")
		}
	}
}
