package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/ipfs/boxo/files"
	"github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const EventChallengeCreatedType = "gitopia.gitopia.storage.EventChallengeCreated"

type ChallengeEvent struct {
	ChallengeId uint64
	Provider    string
}

func UnmarshalChallengeEvent(eventBuf []byte) ([]ChallengeEvent, error) {
	var events []ChallengeEvent

	// Helper to extract string arrays from json
	extractStringArray := func(key string) ([]string, error) {
		var result []string
		value, _, _, err := jsonparser.Get(eventBuf, "events", EventChallengeCreatedType+"."+key)
		if err != nil {
			if err == jsonparser.KeyPathNotFoundError {
				return result, nil // Not found is not an error here
			}
			return nil, err
		}
		jsonparser.ArrayEach(value, func(v []byte, dt jsonparser.ValueType, offset int, err error) {
			result = append(result, string(v))
		})
		return result, nil
	}

	challengeIDs, err := extractStringArray("challenge_id")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing challenge_id")
	}

	providers, err := extractStringArray("provider")
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	// Basic validation
	if len(challengeIDs) == 0 {
		return events, nil // No events to process
	}

	if len(challengeIDs) != len(providers) {
		return nil, errors.New("mismatched attribute array lengths for ChallengeEvent")
	}

	for i := 0; i < len(challengeIDs); i++ {
		challengeId, err := strconv.ParseUint(strings.Trim(challengeIDs[i], `"`), 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing challenge id")
		}

		events = append(events, ChallengeEvent{
			ChallengeId: challengeId,
			Provider:    strings.Trim(providers[i], `"`),
		})
	}

	return events, nil
}

type ChallengeEventHandler struct {
	gc *app.GitopiaProxy
}

func NewChallengeEventHandler(g *app.GitopiaProxy) *ChallengeEventHandler {
	return &ChallengeEventHandler{g}
}

func (h *ChallengeEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	events, err := UnmarshalChallengeEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"challenge_id": event.ChallengeId,
				"provider":     event.Provider,
			}).WithError(err).Error("failed to process ChallengeEvent")
		}
	}

	return nil
}

func (h *ChallengeEventHandler) Process(ctx context.Context, event ChallengeEvent) error {
	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challenge_id": event.ChallengeId,
		"provider":     event.Provider,
	}).Info("processing challenge event")

	// Get challenge details
	challenge, err := h.gc.Challenge(ctx, event.ChallengeId)
	if err != nil {
		return errors.WithMessage(err, "failed to get challenge")
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challenge_id":   event.ChallengeId,
		"provider":       event.Provider,
		"challenge_type": challenge.ChallengeType,
		"content_id":     challenge.ContentId,
		"chunk_index":    challenge.ChunkIndex,
	}).Info("challenge details retrieved")

	// Get packfile from IPFS using challenge CID
	api, err := rpc.NewURLApiWithClient(fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")), &http.Client{})
	if err != nil {
		return errors.WithMessage(err, "failed to create IPFS API")
	}

	var cid string
	switch challenge.ChallengeType {
	case storagetypes.ChallengeType_CHALLENGE_TYPE_PACKFILE:
		packfile, err := h.gc.Packfile(ctx, challenge.ContentId)
		if err != nil {
			return errors.WithMessage(err, "failed to get packfile from Gitopia")
		}
		cid = packfile.Cid
	case storagetypes.ChallengeType_CHALLENGE_TYPE_RELEASE_ASSET:
		release, err := h.gc.ReleaseAsset(ctx, challenge.ContentId)
		if err != nil {
			return errors.WithMessage(err, "failed to get release asset from Gitopia")
		}
		cid = release.Cid
	case storagetypes.ChallengeType_CHALLENGE_TYPE_LFS_OBJECT:
		lfsObject, err := h.gc.LFSObject(ctx, challenge.ContentId)
		if err != nil {
			return errors.WithMessage(err, "failed to get lfs object from Gitopia")
		}
		cid = lfsObject.Cid
	default:
		return errors.New("invalid challenge type")
	}

	p, err := path.NewPath("/ipfs/" + cid)
	if err != nil {
		return errors.WithMessage(err, "failed to create path")
	}

	f, err := api.Unixfs().Get(ctx, p)
	if err != nil {
		return errors.WithMessage(err, "failed to get packfile from IPFS")
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"cid": cid,
	}).Info("content retrieved from IPFS daemon")

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid content format")
	}

	proof, root, chunkHash, err := merkleproof.GenerateProof(file, challenge.ChunkIndex)
	if err != nil {
		return errors.WithMessage(err, "failed to generate proofs")
	}

	// Verify root hash matches
	if !bytes.Equal(root, challenge.RootHash) {
		return errors.New("root hash mismatch")
	}

	// Submit challenge response
	err = h.gc.SubmitChallenge(ctx,
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
				"provider":    event.Provider,
			}).Error("challenge deadline exceeded")
		} else {
			return errors.WithMessage(err, "failed to submit challenge response")
		}
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"challengeId": event.ChallengeId,
		"provider":    event.Provider,
	}).Info("challenge response submitted")

	return nil
}
