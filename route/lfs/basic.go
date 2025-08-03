package lfs

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gitopia/gitopia-storage/app"
	lfsutil "github.com/gitopia/gitopia-storage/lfs"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/ipfs/boxo/files"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
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
	Storagers         map[lfsutil.Storage]lfsutil.Storager
	GitopiaProxy      *app.GitopiaProxy
	IPFSClusterClient ipfsclusterclient.Client
}

// DefaultStorager returns the default storage backend.
func (h *BasicHandler) DefaultStorager() lfsutil.Storager {
	return h.Storagers[h.DefaultStorage]
}

// Storager returns the given storage backend.
func (h *BasicHandler) Storager(storage lfsutil.Storage) lfsutil.Storager {
	return h.Storagers[storage]
}

// GET /{repo-id}.git/info/lfs/object/basic/{oid}
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
		log.Errorf("Failed to locate the object [repo_id: %d, oid: %s]: storage %q not found", repoId, oid, s)
		return
	}

	size, err := s.Size(oid)
	if err != nil {
		internalServerError(w)
		log.Errorf("Failed to locate the object [repo_id: %d, oid: %s]: storage %q not found", repoId, oid, s)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)

	err = s.Download(oid, w)
	if err != nil {
		log.Errorf("Failed to download object [oid: %s]: %v", oid, err)
		return
	}
}

// PUT /{owner}/{repo}.git/info/lfs/object/basic/{oid}
func (h *BasicHandler) ServeUploadHandler(w http.ResponseWriter, r *http.Request) {
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

	repo, err := h.GitopiaProxy.Repository(context.Background(), repoId)
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to get repository")
		return
	}

	userQuota, err := h.GitopiaProxy.UserQuota(context.Background(), repo.Owner.Id)
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to get user quota")
		return
	}

	storageParams, err := h.GitopiaProxy.StorageParams(context.Background())
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to get storage params")
		return
	}

	if !storageParams.StoragePricePerMb.IsZero() {
		costInfo, err := utils.CalculateStorageCost(uint64(userQuota.StorageUsed), uint64(r.ContentLength), storageParams)
		if err != nil {
			internalServerError(w)
			log.WithError(err).Error("failed to calculate storage cost")
			return
		}

		if !costInfo.StorageCharge.IsZero() {
			balance, err := h.GitopiaProxy.CosmosBankBalance(context.Background(), repo.Owner.Id, costInfo.StorageCharge.Denom)
			if err != nil {
				internalServerError(w)
				log.WithError(err).Error("failed to get user balance")
				return
			}

			if balance.Amount.LT(costInfo.StorageCharge.Amount) {
				responseJSON(w, http.StatusPaymentRequired, responseError{Message: "insufficient balance for storage charge"})
				return
			}
		}
	}

	s := h.DefaultStorager()

	utils.LockLFSObject(string(oid))
	defer utils.UnlockLFSObject(string(oid))

	size, err := s.Upload(oid, r.Body)
	if err != nil {
		if err == lfsutil.ErrInvalidOID {
			responseJSON(w, http.StatusBadRequest, responseError{
				Message: err.Error(),
			})
		} else {
			internalServerError(w)
			log.Errorf("Failed to upload object [storage: %s, oid: %s]: %v", s.Storage(), oid, err)
		}
		return
	}

	filePath := localStoragePath(string(oid))

	cid, err := utils.PinFile(h.IPFSClusterClient, filePath)
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to pin file to ipfs")
		return
	}

	rootHash, err := calculateMerkleRoot(filePath)
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to calculate merkle root")
		return
	}

	// First update the LFS object
	err = h.GitopiaProxy.ProposeLFSObjectUpdate(context.Background(), repoId, string(oid), cid, rootHash, size)
	if err != nil {
		internalServerError(w)
		log.WithError(err).Error("failed to propose lfs object update")
		return
	}

	// Wait for LFS object update to be confirmed with a timeout of 10 seconds
	err = h.GitopiaProxy.PollForUpdate(context.Background(), func() (bool, error) {
		return h.GitopiaProxy.CheckProposeLFSObjectUpdate(repoId)
	})

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			log.WithError(err).Error("timeout waiting for LFS object update to be confirmed")
		} else {
			log.WithError(err).Error("failed to verify LFS object update")
		}
		internalServerError(w)
		return
	}

	w.WriteHeader(http.StatusOK)

	log.Tracef("[LFS] Object created %q", oid)
}

// POST /{owner}/{repo}.git/info/lfs/object/basic/verify
func (h *BasicHandler) ServeVerifyHandler(w http.ResponseWriter, r *http.Request) {
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

	size, err := s.Size(request.Oid)
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
	askCredentials := func(w http.ResponseWriter) {
		// w.Header().Set("Lfs-Authenticate", `Basic realm="Git LFS"`)
		responseJSON(w, http.StatusUnauthorized, responseError{
			Message: "Credentials needed",
		})
	}

	return func(w http.ResponseWriter, r *http.Request) {
		username, password := utils.DecodeBasic(r.Header)
		if password == "" {
			askCredentials(w)
			return
		}

		allow, err := utils.ValidateBasicAuth(r, username, password)
		if !allow || err != nil {
			if err != nil {
				log.WithFields(log.Fields{
					"username": username,
					"error":    err.Error(),
				}).Error("Failed to authenticate user")
			}

			w.WriteHeader(http.StatusUnauthorized)
			return
		}

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

func localStoragePath(oid string) string {
	return filepath.Join(viper.GetString("LFS_OBJECTS_DIR"), oid)
}

func calculateMerkleRoot(filePath string) ([]byte, error) {
	// Open the file for merkle root calculation
	file, err := os.Open(filePath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open file: %s", filePath)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get file stat: %s", filePath)
	}

	// Create a files.File from the os.File
	ipfsFile, err := files.NewReaderPathFile(filePath, file, stat)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create files.File from file: %s", filePath)
	}

	// Calculate merkle root
	rootHash, err := merkleproof.ComputeMerkleRoot(ipfsFile)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to compute merkle root for file: %s", filePath)
	}

	return rootHash, nil
}
