package lfs

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	lfsutil "github.com/gitopia/git-server/lfs"
	"github.com/gitopia/git-server/utils"
	log "github.com/sirupsen/logrus"
)

const transferBasic = "basic"
const (
	basicOperationUpload   = "upload"
	basicOperationDownload = "download"
)

type BasicHandler struct {
	// The default storage backend for uploading new objects.
	DefaultStorage lfsutil.Storage
	// The list of available storage backends to access objects.
	Storagers map[lfsutil.Storage]lfsutil.Storager
}

// DefaultStorager returns the default storage backend.
func (h *BasicHandler) DefaultStorager() lfsutil.Storager {
	return h.Storagers[h.DefaultStorage]
}

// Storager returns the given storage backend.
func (h *BasicHandler) Storager(storage lfsutil.Storage) lfsutil.Storager {
	return h.Storagers[storage]
}

// GET /{owner}/{repo}.git/info/lfs/object/basic/{oid}
func (h *BasicHandler) ServeDownloadHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: err.Error(),
		})
		return
	}

	components := strings.Split(r.URL.Path, "/")
	if len(components) != 7 {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Invalid oid",
		})
		return
	}
	oid := lfsutil.OID(components[6])

	s := h.DefaultStorager()
	if s == nil {
		internalServerError(w)
		log.Error("Failed to locate the object [repo_id: %d, oid: %s]: storage %q not found", repoId, oid, s)
		return
	}

	size, err := s.Size(oid)
	if err != nil {
		internalServerError(w)
		log.Error("Failed to locate the object [repo_id: %d, oid: %s]: storage %q not found", repoId, oid, s)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)

	err = s.Download(oid, w)
	if err != nil {
		log.Error("Failed to download object [oid: %s]: %v", oid, err)
		return
	}
}

// PUT /{owner}/{repo}.git/info/lfs/object/basic/{oid}
func (h *BasicHandler) ServeUploadHandler(w http.ResponseWriter, r *http.Request) {
	components := strings.Split(r.URL.Path, "/")
	if len(components) != 7 {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Invalid oid",
		})
		return
	}
	oid := lfsutil.OID(components[6])

	s := h.DefaultStorager()

	// NOTE: LFS client will retry upload the same object if there was a partial failure,
	// therefore we would like to skip ones that already exist.
	_, err := s.Size(oid)
	if err == nil {
		// Object exists, drain the request body and we're good.
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		w.WriteHeader(http.StatusOK)
		return
	}

	_, err = s.Upload(oid, r.Body)
	if err != nil {
		if err == lfsutil.ErrInvalidOID {
			responseJSON(w, http.StatusBadRequest, responseError{
				Message: err.Error(),
			})
		} else {
			internalServerError(w)
			log.Error("Failed to upload object [storage: %s, oid: %s]: %v", s.Storage(), oid, err)
		}
		return
	}

	w.WriteHeader(http.StatusOK)

	log.Trace("[LFS] Object created %q", oid)
}

// POST /{owner}/{repo}.git/info/lfs/object/basic/verify
func (h *BasicHandler) ServeVerifyHandler(w http.ResponseWriter, r *http.Request) {
	components := strings.Split(r.URL.Path, "/")
	if len(components) != 7 {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Invalid oid",
		})
		return
	}
	oid := lfsutil.OID(components[6])

	var request basicVerifyRequest
	defer r.Body.Close()

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: err.Error(),
		})
		return
	}

	if !lfsutil.ValidOID(request.Oid) {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Invalid oid",
		})
		return
	}

	s := h.DefaultStorager()

	size, err := s.Size(oid)
	if err != nil {
		responseJSON(w, http.StatusNotFound, responseError{
			Message: "Object does not exist",
		})
		return
	}

	if size != request.Size {
		responseJSON(w, http.StatusBadRequest, responseError{
			Message: "Object size mismatch",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
}

type basicVerifyRequest struct {
	Oid  lfsutil.OID `json:"oid"`
	Size int64       `json:"size"`
}

// Authenticate tries to authenticate user via HTTP Basic Auth. It first tries to authenticate
// as plain username and password, then use username as access token if previous step failed.
func Authenticate(h http.HandlerFunc) http.HandlerFunc {
	// askCredentials := func(w http.ResponseWriter) {
	// 	// w.Header().Set("Lfs-Authenticate", `Basic realm="Git LFS"`)
	// 	responseJSON(w, http.StatusUnauthorized, responseError{
	// 		Message: "Credentials needed",
	// 	})
	// }

	return func(w http.ResponseWriter, r *http.Request) {
		// _, err := utils.GetCredential(r)
		// if err != nil {
		// 	askCredentials(w)
		// 	return
		// }

		h(w, r)
	}
}

// VerifyOID checks if the ":oid" URL parameter is valid.
func VerifyOID(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		components := strings.Split(r.URL.Path, "/")
		if len(components) != 7 {
			responseJSON(w, http.StatusBadRequest, responseError{
				Message: "Invalid oid",
			})
			return
		}
		oid := lfsutil.OID(components[6])
		if !lfsutil.ValidOID(oid) {
			responseJSON(w, http.StatusBadRequest, responseError{
				Message: "Invalid oid",
			})
			return
		}
		h(w, r)
	}
}

func internalServerError(w http.ResponseWriter) {
	responseJSON(w, http.StatusInternalServerError, responseError{
		Message: "Internal server error",
	})
}
