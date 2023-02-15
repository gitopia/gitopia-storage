package lfs

import (
	"encoding/json"
	"fmt"
	"net/http"

	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/utils"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// batchRequest defines the request payload for the batch endpoint.
type batchRequest struct {
	Operation string `json:"operation"`
	Objects   []struct {
		Oid  lfsutil.OID `json:"oid"`
		Size int64       `json:"size"`
	} `json:"objects"`
}

type batchError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type batchAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header,omitempty"`
}

type batchActions struct {
	Download *batchAction `json:"download,omitempty"`
	Upload   *batchAction `json:"upload,omitempty"`
	Verify   *batchAction `json:"verify,omitempty"`
	Error    *batchError  `json:"error,omitempty"`
}

type batchObject struct {
	Oid     lfsutil.OID  `json:"oid"`
	Size    int64        `json:"size"`
	Actions batchActions `json:"actions"`
}

// batchResponse defines the response payload for the batch endpoint.
type batchResponse struct {
	Transfer string        `json:"transfer"`
	Objects  []batchObject `json:"objects"`
}

type responseError struct {
	Message string `json:"message"`
}

const contentType = "application/vnd.git-lfs+json"

func responseJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)

	err := json.NewEncoder(w).Encode(v)
	if err != nil {
		log.Error("Failed to encode JSON: %v", err)
		return
	}
}

// POST /{repo_id}.git/info/lfs/object/batch
func (h *BasicHandler) ServeBatchHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: err.Error(),
		})
		return
	}

	var request batchRequest
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: err.Error(),
		})
		return
	}

	// NOTE: We only support basic transfer as of now.
	transfer := transferBasic
	// Example: https://server.gitopia.com/1.git/info/lfs/object/basic
	baseHref := fmt.Sprintf("%s/%d.git/info/lfs/objects/basic", viper.GetString("GIT_SERVER_EXTERNAL_ADDR"), repoId)

	objects := make([]batchObject, 0, len(request.Objects))
	switch request.Operation {
	case basicOperationUpload:
		for _, obj := range request.Objects {
			var actions batchActions
			if lfsutil.ValidOID(obj.Oid) {
				actions = batchActions{
					Upload: &batchAction{
						Href: fmt.Sprintf("%s/%s", baseHref, obj.Oid),
						Header: map[string]string{
							// NOTE: git-lfs v2.5.0 sets the Content-Type based on the uploaded file.
							// This ensures that the client always uses the designated value for the header.
							"Content-Type": "application/octet-stream",
						},
					},
					Verify: &batchAction{
						Href: fmt.Sprintf("%s/verify", baseHref),
					},
				}
			} else {
				actions = batchActions{
					Error: &batchError{
						Code:    http.StatusUnprocessableEntity,
						Message: "Object has invalid oid",
					},
				}
			}

			objects = append(objects, batchObject{
				Oid:     obj.Oid,
				Size:    obj.Size,
				Actions: actions,
			})
		}

	case basicOperationDownload:
		s := h.DefaultStorager()
		storedSet := make(map[lfsutil.OID]int64, 0)
		for _, obj := range request.Objects {
			if size, err := s.Size(obj.Oid); err != nil {
				storedSet[obj.Oid] = size
			}
		}

		for _, obj := range request.Objects {
			var actions batchActions
			if size := storedSet[obj.Oid]; size != 0 {
				if size != obj.Size {
					actions.Error = &batchError{
						Code:    http.StatusUnprocessableEntity,
						Message: "Object size mismatch",
					}
				} else {
					actions.Download = &batchAction{
						Href: fmt.Sprintf("%s/%s", baseHref, obj.Oid),
					}
				}
			} else {
				actions.Error = &batchError{
					Code:    http.StatusNotFound,
					Message: "Object does not exist",
				}
			}

			objects = append(objects, batchObject{
				Oid:     obj.Oid,
				Size:    obj.Size,
				Actions: actions,
			})
		}

	default:
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Operation not recognized",
		})
		return
	}

	responseJSON(w, http.StatusOK, batchResponse{
		Transfer: transfer,
		Objects:  objects,
	})
}
