package lfs

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gitopia/gitopia-storage/utils"
	log "github.com/sirupsen/logrus"
)

// Lock represents a Git LFS lock
type Lock struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	LockedAt time.Time `json:"locked_at"`
	Owner    *User     `json:"owner"`
}

// User represents a Git LFS user
type User struct {
	Name string `json:"name"`
}

// LockRequest represents a request to create a lock
type LockRequest struct {
	Path string `json:"path"`
	Ref  *Ref   `json:"ref,omitempty"`
}

// Ref represents a Git reference
type Ref struct {
	Name string `json:"name"`
}

// LockResponse represents the response for lock operations
type LockResponse struct {
	Lock    *Lock   `json:"lock,omitempty"`
	Locks   []*Lock `json:"locks,omitempty"`
	Message string  `json:"message,omitempty"`
}

// UnlockRequest represents a request to unlock a file
type UnlockRequest struct {
	Force bool `json:"force,omitempty"`
	Ref   *Ref `json:"ref,omitempty"`
}

// VerifyLocksRequest represents a request to verify locks
type VerifyLocksRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Ref    *Ref   `json:"ref,omitempty"`
}

// VerifyLocksResponse represents the response for lock verification
type VerifyLocksResponse struct {
	Ours   []*Lock `json:"ours"`
	Theirs []*Lock `json:"theirs"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// LockHandler handles Git LFS lock operations
type LockHandler struct {
	// In a real implementation, you'd want to store locks in a database
	// For now, we'll just return empty responses to satisfy the Git LFS client
}

// POST /{repo-id}.git/info/lfs/locks
func (h *LockHandler) ServeCreateLockHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		log.WithError(err).Error("Failed to parse repository ID from URI")
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}

	var lockReq LockRequest
	if err := json.NewDecoder(r.Body).Decode(&lockReq); err != nil {
		log.WithError(err).Error("Failed to decode lock request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.WithFields(log.Fields{
		"repo_id": repoId,
		"path":    lockReq.Path,
	}).Info("Lock request received")

	// For now, return a successful response without actually creating a lock
	// In a real implementation, you'd store this in a database
	response := LockResponse{
		Lock: &Lock{
			ID:       "dummy-lock-id",
			Path:     lockReq.Path,
			LockedAt: time.Now(),
			Owner: &User{
				Name: "user",
			},
		},
	}

	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// GET /{repo-id}.git/info/lfs/locks
func (h *LockHandler) ServeListLocksHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		log.WithError(err).Error("Failed to parse repository ID from URI")
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}

	log.WithField("repo_id", repoId).Info("List locks request received")

	// Return empty list of locks
	response := LockResponse{
		Locks: []*Lock{},
	}

	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	json.NewEncoder(w).Encode(response)
}

// POST /{repo-id}.git/info/lfs/locks/{id}/unlock
func (h *LockHandler) ServeUnlockHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		log.WithError(err).Error("Failed to parse repository ID from URI")
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}

	var unlockReq UnlockRequest
	if err := json.NewDecoder(r.Body).Decode(&unlockReq); err != nil {
		log.WithError(err).Error("Failed to decode unlock request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.WithField("repo_id", repoId).Info("Unlock request received")

	// Return successful unlock response
	response := LockResponse{
		Lock: &Lock{
			ID:       "dummy-lock-id",
			Path:     "/dummy/path",
			LockedAt: time.Now(),
			Owner: &User{
				Name: "user",
			},
		},
	}

	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	json.NewEncoder(w).Encode(response)
}

// POST /{repo-id}.git/info/lfs/locks/verify
func (h *LockHandler) ServeVerifyLocksHandler(w http.ResponseWriter, r *http.Request) {
	repoId, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		log.WithError(err).Error("Failed to parse repository ID from URI")
		http.Error(w, "Invalid repository ID", http.StatusBadRequest)
		return
	}

	var verifyReq VerifyLocksRequest
	if err := json.NewDecoder(r.Body).Decode(&verifyReq); err != nil {
		log.WithError(err).Error("Failed to decode verify locks request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	log.WithField("repo_id", repoId).Info("Verify locks request received")

	// Return empty verification response
	response := VerifyLocksResponse{
		Ours:   []*Lock{},
		Theirs: []*Lock{},
	}

	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	json.NewEncoder(w).Encode(response)
}
