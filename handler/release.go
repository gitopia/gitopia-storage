package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/ipfs-cluster/ipfs-cluster/api"
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
	RepositoryId      uint64
	RepositoryOwnerId string
	Tag               string
	Attachments       []gitopiatypes.Attachment
	Provider          string
}

func (e *ReleaseEvent) UnMarshal(eventBuf []byte) error {
	repoIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+gitopiatypes.EventAttributeRepoIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repoIdStr = strings.Trim(repoIdStr, "\"")
	repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	repoOwnerId, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+gitopiatypes.EventAttributeRepoOwnerIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository owner id")
	}

	tag, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+gitopiatypes.EventAttributeReleaseTagNameKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tag")
	}
	tag = strings.Trim(tag, "\"")

	attachmentsStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+gitopiatypes.EventAttributeReleaseAttachmentsKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing attachments")
	}
	attachmentsStr = strings.Trim(attachmentsStr, "\"")

	// unmarshal attachments
	var attachments []gitopiatypes.Attachment
	err = json.Unmarshal([]byte(attachmentsStr), &attachments)
	if err != nil {
		return errors.Wrap(err, "error unmarshalling attachments")
	}

	provider, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+gitopiatypes.EventAttributeProviderKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}
	provider = strings.Trim(provider, "\"")

	e.RepositoryId = repoId
	e.RepositoryOwnerId = repoOwnerId
	e.Tag = tag
	e.Attachments = attachments
	e.Provider = provider
	return nil
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
	event := &ReleaseEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	err = h.Process(ctx, *event, eventType)
	if err != nil {
		return errors.WithMessage(err, "error processing event")
	}

	return nil
}

func (h *ReleaseEventHandler) pinAttachment(ctx context.Context, attachment gitopiatypes.Attachment) (string, error) {
	// Get attachment file path from attachment directory
	attachmentDir := viper.GetString("ATTACHMENT_DIR")
	filePath := fmt.Sprintf("%s/%s", attachmentDir, attachment.Sha)

	// Add and pin the file to IPFS cluster
	paths := []string{filePath}
	addParams := api.DefaultAddParams()
	addParams.Recursive = false
	addParams.Layout = "balanced"

	outputChan := make(chan api.AddedOutput)
	var cid api.Cid

	go func() {
		err := h.ipfsClusterClient.Add(ctx, paths, addParams, outputChan)
		if err != nil {
			logger.FromContext(ctx).WithError(err).WithField("attachment", attachment.Name).Error("failed to add file to IPFS cluster")
			close(outputChan)
		}
	}()

	// Get CID from output channel
	for output := range outputChan {
		cid = output.Cid
	}

	// Pin the file with default options
	pinOpts := api.PinOptions{
		ReplicationFactorMin: -1,
		ReplicationFactorMax: -1,
		Name:                 attachment.Name,
	}

	_, err := h.ipfsClusterClient.Pin(ctx, cid, pinOpts)
	if err != nil {
		return "", errors.Wrap(err, "failed to pin file in IPFS cluster")
	}

	return cid.String(), nil
}

func (h *ReleaseEventHandler) unpinAttachment(ctx context.Context, asset storagetypes.ReleaseAsset) error {
	// Parse CID from string
	cid, err := api.DecodeCid(asset.Cid)
	if err != nil {
		return errors.Wrap(err, "failed to parse CID")
	}

	// Unpin the file from IPFS cluster
	_, err = h.ipfsClusterClient.Unpin(ctx, cid)
	if err != nil {
		return errors.Wrap(err, "failed to unpin file from IPFS cluster")
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
		// Pin all new attachments
		for _, attachment := range event.Attachments {
			cid, err := h.pinAttachment(ctx, attachment)
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

			err = h.gc.UpdateReleaseAsset(ctx, event.RepositoryId, event.Tag, attachment.Name, cid, rootHash, int64(attachment.Size_), attachment.Sha)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to update release asset")
				continue
			}

			// Wait for release asset update to be confirmed with a timeout of 10 seconds
			err = h.gc.PollForUpdate(ctx, func() (bool, error) {
				return h.gc.CheckReleaseAssetUpdate(event.RepositoryId, event.Tag, attachment.Name, cid)
			})

			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.FromContext(ctx).WithError(err).Error("timeout waiting for release asset update to be confirmed")
				} else {
					logger.FromContext(ctx).WithError(err).Error("failed to verify release asset update")
				}
				continue
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"attachment":    attachment.Name,
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("updated release asset")

		}

	case EventUpdateReleaseType:
		// Compare existing and new attachments
		// Unpin removed attachments
		for _, existingAsset := range existingAssets {
			found := false
			for _, newAttachment := range event.Attachments {
				if existingAsset.Name == newAttachment.Name {
					found = true
					break
				}
			}
			if !found {
				// Delete attachment
				err := h.gc.DeleteReleaseAsset(ctx, event.RepositoryId, event.Tag, existingAsset.Name, event.RepositoryOwnerId)
				if err != nil {
					logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
						"asset":         existingAsset.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Error("failed to delete release attachment")
					continue
				}

				// Wait for release asset delete to be confirmed with a timeout of 10 seconds
				err = h.gc.PollForUpdate(ctx, func() (bool, error) {
					return h.gc.CheckReleaseAssetDelete(event.RepositoryId, event.Tag, existingAsset.Name)
				})

				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						logger.FromContext(ctx).WithError(err).Error("timeout waiting for release asset delete to be confirmed")
					} else {
						logger.FromContext(ctx).WithError(err).Error("failed to verify release asset delete")
					}
					continue
				}

				logger.FromContext(ctx).WithFields(logrus.Fields{
					"asset":         existingAsset.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Info("deleted release attachment")

				// Get attachment reference count
				refCount, err := h.gc.StorageCidReferenceCount(ctx, existingAsset.Cid)
				if err != nil {
					logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
						"asset":         existingAsset.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Error("failed to get attachment reference count")
					continue
				}
				if refCount == 0 {
					if err := h.unpinAttachment(ctx, existingAsset); err != nil {
						logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
							"asset":         existingAsset.Name,
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
						}).Error("failed to unpin release attachment")
						continue
					}

					logger.FromContext(ctx).WithFields(logrus.Fields{
						"asset":         existingAsset.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Info("unpinned release attachment")
				}
			}
		}

		// Pin new attachments and update if CID changed
		for _, attachment := range event.Attachments {
			existingAsset, exists := existingAssets[attachment.Name]
			if exists {
				// Pin the new attachment
				newCid, err := h.pinAttachment(ctx, attachment)
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

				// If CID is different, unpin old one and update
				if newCid != existingAsset.Cid {
					rootHash, err := h.calculateMerkleRoot(ctx, attachment)
					if err != nil {
						logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
							"attachment":    attachment.Name,
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
						}).Error("failed to calculate merkle root")
						continue
					}

					// Update the release asset with new CID and merkle root
					err = h.gc.UpdateReleaseAsset(ctx, event.RepositoryId, event.Tag, attachment.Name, newCid, rootHash, int64(attachment.Size_), attachment.Sha)
					if err != nil {
						logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
							"attachment":    attachment.Name,
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
						}).Error("failed to update release asset")
						continue
					}

					// Wait for release asset update to be confirmed with a timeout of 10 seconds
					err = h.gc.PollForUpdate(ctx, func() (bool, error) {
						return h.gc.CheckReleaseAssetUpdate(event.RepositoryId, event.Tag, attachment.Name, newCid)
					})

					if err != nil {
						if errors.Is(err, context.DeadlineExceeded) {
							logger.FromContext(ctx).WithError(err).Error("timeout waiting for release asset update to be confirmed")
						} else {
							logger.FromContext(ctx).WithError(err).Error("failed to verify release asset update")
						}
						continue
					}

					logger.FromContext(ctx).WithFields(logrus.Fields{
						"attachment":    attachment.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Info("updated release asset")

					// Check reference count
					refCount, err := h.gc.StorageCidReferenceCount(ctx, existingAsset.Cid)
					if err != nil {
						logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
							"asset":         existingAsset.Name,
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
						}).Error("failed to get release attachment reference count")
						continue
					}
					if refCount == 0 {
						if err := h.unpinAttachment(ctx, existingAsset); err != nil {
							logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
								"asset":         existingAsset.Name,
								"repository_id": event.RepositoryId,
								"tag":           event.Tag,
							}).Error("failed to unpin old release attachment")
							continue
						}

						logger.FromContext(ctx).WithFields(logrus.Fields{
							"attachment":    attachment.Name,
							"repository_id": event.RepositoryId,
							"tag":           event.Tag,
						}).Info("unpinned old release attachment")
					}
				}
			} else {
				// This is a completely new attachment
				newCid, err := h.pinAttachment(ctx, attachment)
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

				// Update the release asset with new CID and merkle root
				err = h.gc.UpdateReleaseAsset(ctx, event.RepositoryId, event.Tag, attachment.Name, newCid, rootHash, int64(attachment.Size_), attachment.Sha)
				if err != nil {
					logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
						"attachment":    attachment.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Error("failed to update release asset")
					continue
				}

				// Wait for release asset update to be confirmed with a timeout of 10 seconds
				err = h.gc.PollForUpdate(ctx, func() (bool, error) {
					return h.gc.CheckReleaseAssetUpdate(event.RepositoryId, event.Tag, attachment.Name, newCid)
				})

				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						logger.FromContext(ctx).WithError(err).Error("timeout waiting for release asset update to be confirmed")
					} else {
						logger.FromContext(ctx).WithError(err).Error("failed to verify release asset update")
					}
					continue
				}

				logger.FromContext(ctx).WithFields(logrus.Fields{
					"attachment":    attachment.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Info("updated release asset")
			}
		}

	case EventDeleteReleaseType:
		// Unpin all attachments
		for _, existingAsset := range existingAssets {
			// Delete the release asset
			err := h.gc.DeleteReleaseAsset(ctx, event.RepositoryId, event.Tag, existingAsset.Name, event.RepositoryOwnerId)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"asset":         existingAsset.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to delete release asset")
				continue
			}

			// Wait for release asset delete to be confirmed with a timeout of 10 seconds
			err = h.gc.PollForUpdate(ctx, func() (bool, error) {
				return h.gc.CheckReleaseAssetDelete(event.RepositoryId, event.Tag, existingAsset.Name)
			})

			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					logger.FromContext(ctx).WithError(err).Error("timeout waiting for release asset delete to be confirmed")
				} else {
					logger.FromContext(ctx).WithError(err).Error("failed to verify release asset delete")
				}
				continue
			}

			logger.FromContext(ctx).WithFields(logrus.Fields{
				"asset":         existingAsset.Name,
				"repository_id": event.RepositoryId,
				"tag":           event.Tag,
			}).Info("deleted release asset")

			// Check reference count
			refCount, err := h.gc.StorageCidReferenceCount(ctx, existingAsset.Cid)
			if err != nil {
				logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
					"asset":         existingAsset.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Error("failed to get release attachment reference count")
				continue
			}
			if refCount == 0 {
				// Unpin from IPFS cluster
				if err := h.unpinAttachment(ctx, existingAsset); err != nil {
					logger.FromContext(ctx).WithError(err).WithFields(logrus.Fields{
						"asset":         existingAsset.Name,
						"repository_id": event.RepositoryId,
						"tag":           event.Tag,
					}).Error("failed to unpin release attachment")
					continue
				}

				logger.FromContext(ctx).WithFields(logrus.Fields{
					"asset":         existingAsset.Name,
					"repository_id": event.RepositoryId,
					"tag":           event.Tag,
				}).Info("unpinned release attachment")
			}
		}
	}

	return nil
}
