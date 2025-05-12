package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/pkg/merkleproof"
	"github.com/gitopia/gitopia-go/logger"
	storagetypes "github.com/gitopia/gitopia/v5/x/storage/types"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventChallengeCreatedType = "gitopia.gitopia.storage.EventChallengeCreated"

type ChallengeEvent struct {
	ChallengeId     uint64
	ProviderAddress string
}

func (e *ChallengeEvent) UnMarshal(eventBuf []byte) error {
	challengeIdStr, err := jsonparser.GetString(eventBuf, "events", EventChallengeCreatedType+".challenge_id", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing challenge id")
	}
	challengeIdStr = strings.Trim(challengeIdStr, "\"")
	challengeId, err := strconv.ParseUint(challengeIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing challenge id")
	}

	providerAddress, err := jsonparser.GetString(eventBuf, "events", EventChallengeCreatedType+".provider_address", "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider address")
	}
	providerAddress = strings.Trim(providerAddress, "\"")

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
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challengeId": event.ChallengeId,
		"provider":    event.ProviderAddress,
	}).Info("processing challenge event")

	// Get challenge details
	challenge, err := h.gc.Challenge(ctx, event.ChallengeId)
	if err != nil {
		return errors.WithMessage(err, "failed to get challenge")
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challengeId": event.ChallengeId,
		"provider":    event.ProviderAddress,
		"packfileId":  challenge.PackfileId,
		"chunkIndex":  challenge.ChunkIndex,
		"rootHash":    challenge.RootHash,
	}).Info("challenge details retrieved")

	// Get packfile from IPFS using challenge CID
	api, err := rpc.NewURLApiWithClient(fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")), &http.Client{})
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

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"cid": packfile.Cid,
	}).Info("packfile retrieved from IPFS daemon")

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid packfile format")
	}

	proof, root, chunkHash, err := merkleproof.GenerateChunkProof(file, challenge.ChunkIndex, 256*1024)
	if err != nil {
		return errors.WithMessage(err, "failed to generate proofs")
	}

	// Verify root hash matches
	if !bytes.Equal(root, challenge.RootHash) {
		return errors.New("root hash mismatch")
	}

	// Submit challenge response
	err = h.gc.SubmitChallenge(ctx,
		event.ProviderAddress,
		event.ChallengeId,
		chunkHash,
		&storagetypes.Proof{
			Hashes: proof.Hashes,
			Index:  proof.Index,
		})
	if err != nil {
		if strings.Contains(err.Error(), "challenge deadline exceeded") {
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"challengeId": event.ChallengeId,
				"provider":    event.ProviderAddress,
			}).Error("challenge deadline exceeded")
		} else {
			return errors.WithMessage(err, "failed to submit challenge response")
		}
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challengeId": event.ChallengeId,
		"provider":    event.ProviderAddress,
	}).Info("challenge response submitted")

	return nil
}
