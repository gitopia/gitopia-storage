package handler

import (
	"context"
	"encoding/hex"
	"io"
	"strconv"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/pkg/merkleproof"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type ChallengeEvent struct {
	ChallengeId     uint64
	ProviderAddress string
}

func (e *ChallengeEvent) UnMarshal(eventBuf []byte) error {
	challengeIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+".challenge_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing challenge id")
	}
	challengeId, err := strconv.ParseUint(challengeIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing challenge id")
	}

	providerAddress, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+".provider_address", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider address")
	}

	e.ChallengeId = challengeId
	e.ProviderAddress = providerAddress

	return nil
}

type ChallengeEventHandler struct {
	gc app.GitopiaProxy
}

func NewChallengeEventHandler(g app.GitopiaProxy) ChallengeEventHandler {
	return ChallengeEventHandler{g}
}

func (h *ChallengeEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &ChallengeEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	// Only process challenges meant for this provider
	if !h.gc.IsProviderChallenge(event.ProviderAddress) {
		return nil
	}

	return h.Process(ctx, *event)
}

func (h *ChallengeEventHandler) Process(ctx context.Context, event ChallengeEvent) error {
	logger := logger.FromContext(ctx).WithFields(logrus.Fields{
		"challengeId": event.ChallengeId,
		"provider":    event.ProviderAddress,
	})
	logger.Info("Processing challenge event")

	// Get challenge details
	challenge, err := h.gc.Challenge(ctx, event.ChallengeId)
	if err != nil {
		return errors.WithMessage(err, "failed to get challenge")
	}

	// Get packfile from IPFS using challenge CID
	api, err := rpc.NewLocalApi()
	if err != nil {
		return errors.WithMessage(err, "failed to create IPFS API")
	}

	packfile, err := h.gc.Packfile(ctx, challenge.PackfileId)
	if err != nil {
		return errors.WithMessage(err, "failed to get packfile from Gitopia")
	}

	p, err := path.NewPath("/ipfs/" + packfile.Cid)
	if err != nil {
		return errors.WithMessage(err, "failed to create path")
	}

	f, err := api.Unixfs().Get(ctx, p)
	if err != nil {
		return errors.WithMessage(err, "failed to get packfile from IPFS")
	}

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid packfile format")
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return errors.WithMessage(err, "failed to read packfile data")
	}

	// Generate proof for the challenged chunk
	chunks := splitIntoChunks(data, 256*1024) // 256KiB chunks
	proofs, root, err := merkleproof.GeneratePackfileProofs(chunks)
	if err != nil {
		return errors.WithMessage(err, "failed to generate proofs")
	}

	// Verify root hash matches
	if hex.EncodeToString(root) != challenge.RootHash {
		return errors.New("root hash mismatch")
	}

	// Get proof for challenged chunk
	if int(challenge.ChunkIndex) >= len(proofs) {
		return errors.New("invalid chunk index")
	}
	proof := proofs[challenge.ChunkIndex]

	// Submit challenge response
	err = h.gc.SubmitChallenge(ctx,
		event.ProviderAddress,
		event.ChallengeId,
		proof.ChunkData,
		proof.Proof.Hashes)
	if err != nil {
		return errors.WithMessage(err, "failed to submit challenge response")
	}

	logger.Info("Challenge response submitted successfully")
	return nil
}

// Helper function to split data into chunks
func splitIntoChunks(data []byte, chunkSize int) [][]byte {
	var chunks [][]byte
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}
	return chunks
}
