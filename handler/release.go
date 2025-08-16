package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/ipfs/boxo/files"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	EventCreateReleaseType = "CreateRelease"
	EventUpdateReleaseType = "UpdateRelease"
	EventDeleteReleaseType = "DeleteRelease"
)

type ReleaseEvent struct {
	Creator           string
	RepositoryId      uint64
	RepositoryOwnerId string
	Tag               string
	Attachments       []gitopiatypes.Attachment
	Provider          string
}

func UnmarshalReleaseEvent(eventBuf []byte) ([]ReleaseEvent, error) {
	var events []ReleaseEvent

	creators, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeCreatorKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing creator")
	}

	repoIDs, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeRepoIdKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository id")
	}

	repoOwnerIDs, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeRepoOwnerIdKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing repository owner id")
	}

	tags, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeReleaseTagNameKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing tag")
	}

	attachmentsArray, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeReleaseAttachmentsKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing attachments")
	}

	providers, err := ExtractStringArray(eventBuf, "message", gitopiatypes.EventAttributeProviderKey)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing provider")
	}

	// Basic validation
	if len(creators) == 0 {
		return events, nil // No events to process
	}

	if !(len(creators) == len(repoIDs) && len(creators) == len(repoOwnerIDs) && len(creators) == len(tags) && len(creators) == len(attachmentsArray) && len(creators) == len(providers)) {
		return nil, errors.New("mismatched attribute array lengths for ReleaseEvent")
	}

	for i := 0; i < len(creators); i++ {
		repoId, err := strconv.ParseUint(repoIDs[i], 10, 64)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing repository id")
		}

		var attachments []gitopiatypes.Attachment
		attachmentsStr := attachmentsArray[i]
		err = json.Unmarshal([]byte(attachmentsStr), &attachments)
		if err != nil {
			return nil, errors.Wrap(err, "error unmarshalling attachments")
		}

		events = append(events, ReleaseEvent{
			Creator:           creators[i],
			RepositoryId:      repoId,
			RepositoryOwnerId: repoOwnerIDs[i],
			Tag:               tags[i],
			Attachments:       attachments,
			Provider:          providers[i],
		})
	}

	return events, nil
}

type ReleaseEventHandler struct {
	gc                *app.GitopiaProxy
	ipfsClusterClient ipfsclusterclient.Client
}

func NewReleaseEventHandler(g *app.GitopiaProxy, ipfsClusterClient ipfsclusterclient.Client) ReleaseEventHandler {
	return ReleaseEventHandler{
		gc:                g,
		ipfsClusterClient: ipfsClusterClient,
	}
}

func (h *ReleaseEventHandler) Handle(ctx context.Context, eventBuf []byte, eventType string) error {
	events, err := UnmarshalReleaseEvent(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	for _, event := range events {
		if err := h.Process(ctx, event, eventType); err != nil {
			// Log error and continue processing other events
			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
				"event_type":    eventType,
			}).WithError(err).Error("failed to process ReleaseEvent")
		}
	}

	return nil
}

func (h *ReleaseEventHandler) calculateMerkleRoot(ctx context.Context, attachment gitopiatypes.Attachment) ([]byte, error) {
	// Get attachment file path from attachment directory
	attachmentDir := viper.GetString("ATTACHMENT_DIR")
	filePath := fmt.Sprintf("%s/%s", attachmentDir, attachment.Sha)

	// Open the file for merkle root calculation
	file, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open attachment file: %s", attachment.Name)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get file stat: %s", attachment.Name)
	}

	// Create a files.File from the os.File
	ipfsFile, err := files.NewReaderPathFile(filePath, file, stat)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create files.File from attachment file: %s", attachment.Name)
	}

	// Calculate merkle root
	rootHash, err := merkleproof.ComputeMerkleRoot(ipfsFile)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compute merkle root for attachment: %s", attachment.Name)
	}

	return rootHash, nil
}

func (h *ReleaseEventHandler) Process(ctx context.Context, event ReleaseEvent, eventType string) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"repository_id": event.RepositoryId,
		"tag":           event.Tag,
		"event_type":    eventType,
	}).Info("processing release event")

	attachmentDir := viper.GetString("ATTACHMENT_DIR")

	// Get existing release assets if this is an update or delete event
	existingAssets := make(map[string]storagetypes.ReleaseAsset)
	if eventType == EventUpdateReleaseType || eventType == EventDeleteReleaseType {
		// Query existing release assets
		assets, err := h.gc.RepositoryReleaseAssets(ctx, event.RepositoryId, event.Tag)
		if err != nil && !strings.Contains(err.Error(), "release asset not found") {
			return errors.WithMessage(err, "error querying release assets")
		}

		for _, asset := range assets {
			existingAssets[asset.Name] = asset
		}
	}

	// Handle attachments based on event type
	switch eventType {
	case EventCreateReleaseType:
		// Pin all new attachments and propose a batch update
		var updates []*storagetypes.ReleaseAssetUpdate
		for _, attachment := range event.Attachments {
			filePath := fmt.Sprintf("%s/%s", attachmentDir, attachment.Sha)
			cid, err := utils.PinFile(h.ipfsClusterClient, filePath)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to pin attachment")
				continue
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"attachment":    attachment.Name,
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("pinned release attachment")

			rootHash, err := h.calculateMerkleRoot(ctx, attachment)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to calculate merkle root")
				continue
			}

			updates = append(updates, &storagetypes.ReleaseAssetUpdate{
				Name:      attachment.Name,
				Cid:       cid,
				RootHash:  rootHash,
				Size_:     uint64(attachment.Size_),
				Sha256:    attachment.Sha,
				OldSha256: "",
				OldCid:    "",
			})
		}

		if len(updates) > 0 {
			if err := h.gc.ProposeReleaseAssetsUpdate(ctx, event.Creator, event.RepositoryId, event.Tag, updates); err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to propose release assets update")
				break
			}

			// Wait for proposal to show up
			if err := h.gc.PollForUpdate(ctx, func() (bool, error) {
				return h.gc.CheckProposeReleaseAssetsUpdate(event.RepositoryId, event.Tag, event.Creator)
			}); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.FromContext(ctx).WithError(err).Error("timeout waiting for release assets update proposal")
				} else {
					logger.FromContext(ctx).WithError(err).Error("failed to verify release assets update proposal")
				}
				break
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("proposed release assets update")
		}

	case EventUpdateReleaseType:
		// Pin new/modified attachments and propose a batch update (including deletions)
		var updates []*storagetypes.ReleaseAssetUpdate
		// First, detect deletions
		for name, existingAsset := range existingAssets {
			found := false
			for _, newAttachment := range event.Attachments {
				if name == newAttachment.Name {
					found = true
					break
				}
			}
			if !found {
				updates = append(updates, &storagetypes.ReleaseAssetUpdate{
					Name:      existingAsset.Name,
					OldCid:    existingAsset.Cid,
					OldSha256: existingAsset.Sha256,
					Delete:    true,
				})
			}
		}
		// Then process additions/changes
		for _, attachment := range event.Attachments {
			existingAsset, exists := existingAssets[attachment.Name]
			if exists && existingAsset.Sha256 == attachment.Sha {
				continue
			}

			filePath := fmt.Sprintf("%s/%s", attachmentDir, attachment.Sha)
			newCid, err := utils.PinFile(h.ipfsClusterClient, filePath)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to pin release attachment")
				continue
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"attachment":    attachment.Name,
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("pinned release attachment")

			rootHash, err := h.calculateMerkleRoot(ctx, attachment)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to calculate merkle root")
				continue
			}

			if exists {
				// Only include if CID changed
				if newCid != existingAsset.Cid {
					updates = append(updates, &storagetypes.ReleaseAssetUpdate{
						Name:      attachment.Name,
						Cid:       newCid,
						RootHash:  rootHash,
						Size_:     uint64(attachment.Size_),
						Sha256:    attachment.Sha,
						OldSha256: existingAsset.Sha256,
						OldCid:    existingAsset.Cid,
					})
				}
			} else {
				// New attachment
				updates = append(updates, &storagetypes.ReleaseAssetUpdate{
					Name:      attachment.Name,
					Cid:       newCid,
					RootHash:  rootHash,
					Size_:     uint64(attachment.Size_),
					Sha256:    attachment.Sha,
					OldSha256: "",
					OldCid:    "",
				})
			}
		}

		if len(updates) > 0 {
			if err := h.gc.ProposeReleaseAssetsUpdate(ctx, event.Creator, event.RepositoryId, event.Tag, updates); err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to propose release assets update")
				break
			}

			// Wait for proposal to show up
			if err := h.gc.PollForUpdate(ctx, func() (bool, error) {
				return h.gc.CheckProposeReleaseAssetsUpdate(event.RepositoryId, event.Tag, event.Creator)
			}); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.FromContext(ctx).WithError(err).Error("timeout waiting for release assets update proposal")
				} else {
					logger.FromContext(ctx).WithError(err).Error("failed to verify release assets update proposal")
				}
				break
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("proposed release assets update")
		}

	case EventDeleteReleaseType:
		// Build a delete-only proposal for all existing assets
		var updates []*storagetypes.ReleaseAssetUpdate
		for _, existingAsset := range existingAssets {
			updates = append(updates, &storagetypes.ReleaseAssetUpdate{
				Name:      existingAsset.Name,
				OldCid:    existingAsset.Cid,
				OldSha256: existingAsset.Sha256,
				Delete:    true,
			})
		}
		if len(updates) > 0 {
			if err := h.gc.ProposeReleaseAssetsUpdate(ctx, event.Creator, event.RepositoryId, event.Tag, updates); err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to propose release assets delete update")
				break
			}

			// Wait for proposal to show up
			if err := h.gc.PollForUpdate(ctx, func() (bool, error) {
				return h.gc.CheckProposeReleaseAssetsUpdate(event.RepositoryId, event.Tag, event.Creator)
			}); err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.FromContext(ctx).WithError(err).Error("timeout waiting for release assets delete proposal")
				} else {
					logger.FromContext(ctx).WithError(err).Error("failed to verify release assets delete proposal")
				}
				break
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("proposed release assets delete update")
		}
	}

	return nil
}
