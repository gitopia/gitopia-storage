package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type DaoCreateReleaseEvent struct {
	Admin        string
	RepositoryId uint64
	Tag          string
	Attachments  []types.Attachment
	Provider     string
}

// tm event codec
func (e *DaoCreateReleaseEvent) UnMarshal(eventBuf []byte) error {
	admin, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeCreatorKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing admin")
	}

	repositoryIdStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeRepoIdKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}
	repositoryIdStr = strings.Trim(repositoryIdStr, "\"")
	repositoryId, err := strconv.ParseUint(repositoryIdStr, 10, 64)
	if err != nil {
		return errors.Wrap(err, "error parsing repository id")
	}

	tag, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeReleaseTagNameKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing tag")
	}
	tag = strings.Trim(tag, "\"")

	attachmentsStr, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeReleaseAttachmentsKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing attachments")
	}
	attachmentsStr = strings.Trim(attachmentsStr, "\"")

	// unmarshal attachments
	var attachments []types.Attachment
	err = json.Unmarshal([]byte(attachmentsStr), &attachments)
	if err != nil {
		return errors.Wrap(err, "error unmarshalling attachments")
	}

	provider, err := jsonparser.GetString(eventBuf, "events", sdk.EventTypeMessage+"."+types.EventAttributeProviderKey, "[0]")
	if err != nil {
		return errors.Wrap(err, "error parsing provider")
	}
	provider = strings.Trim(provider, "\"")

	e.Admin = admin
	e.RepositoryId = repositoryId
	e.Tag = tag
	e.Attachments = attachments
	e.Provider = provider

	return nil
}

type DaoCreateReleaseEventHandler struct {
	gc                app.GitopiaProxy
	ipfsClusterClient ipfsclusterclient.Client
}

func NewDaoCreateReleaseEventHandler(g app.GitopiaProxy, ipfsClusterClient ipfsclusterclient.Client) DaoCreateReleaseEventHandler {
	return DaoCreateReleaseEventHandler{
		gc:                g,
		ipfsClusterClient: ipfsClusterClient,
	}
}

func (h *DaoCreateReleaseEventHandler) Handle(ctx context.Context, eventBuf []byte) error {
	event := &DaoCreateReleaseEvent{}
	err := event.UnMarshal(eventBuf)
	if err != nil {
		return errors.WithMessage(err, "event parse error")
	}

	err = h.Process(ctx, *event)
	if err != nil {
		return errors.WithMessage(err, "error processing event")
	}

	return nil
}

func (h *DaoCreateReleaseEventHandler) Process(ctx context.Context, event DaoCreateReleaseEvent) error {
	// Skip processing if message is not meant for this provider
	if !h.gc.CheckProvider(event.Provider) {
		return nil
	}

	logger.FromContext(ctx).WithFields(logrus.Fields{
		"admin":         event.Admin,
		"repository_id": event.RepositoryId,
		"tag":           event.Tag,
	}).Info("Processing dao create release event")

	// Create a release event that will be processed by the release handler
	releaseEvent := ReleaseEvent{
		RepositoryId: event.RepositoryId,
		Tag:          event.Tag,
		Attachments:  event.Attachments,
		Provider:     event.Provider,
	}

	// Create a new ReleaseEventHandler to reuse its Process method
	releaseHandler := NewReleaseEventHandler(h.gc, h.ipfsClusterClient)

	// Process the release event
	return releaseHandler.Process(ctx, releaseEvent, EventCreateReleaseType)
}
