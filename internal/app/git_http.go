package app

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	"github.com/ipfs/boxo/files"
	ipfspath "github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

func (s *Server) GetInfoRefs(_ string, w http.ResponseWriter, r *Request) {
	const logContext = "get-info-refs"
	rpc := r.URL.Query().Get("service")
	defer r.Body.Close()

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", rpc))
	w.Header().Set("Cache-Control", "no-cache")

	if rpc != "git-upload-pack" && rpc != "git-receive-pack" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	repoID, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		utils.LogError(logContext, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	utils.LockRepository(repoID)
	defer utils.UnlockRepository(repoID)

	if err := s.CacheRepository(repoID); err != nil {
		utils.LogError(logContext, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	cmd, pipe := utils.GitCommand(s.Config.GitPath, subCommand(rpc), "--stateless-rpc", "--advertise-refs", r.RepoPath)
	if err := cmd.Start(); err != nil {
		utils.LogError(logContext, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer utils.CleanUpProcessGroup(cmd)

	if err := packLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
		utils.LogError(logContext, err)
		return // Headers already sent
	}

	if err := packFlush(w); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if _, err := io.Copy(w, pipe); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		utils.LogError(logContext, err)
	}
}

func (s *Server) PostRPC(service string, w http.ResponseWriter, r *Request) {
	const logContext = "post-rpc"
	body := r.Body
	defer body.Close()

	if r.Header.Get("Content-Encoding") == "gzip" {
		var err error
		body, err = gzip.NewReader(r.Body)
		if err != nil {
			fail500(w, logContext, err)
			return
		}
		defer body.Close()
	}

	repoID, err := utils.ParseRepositoryIdfromURI(r.URL.Path)
	if err != nil {
		fail500(w, logContext, fmt.Errorf("failed to parse repository id: %v", err))
		return
	}

	utils.LockRepository(repoID)
	defer utils.UnlockRepository(repoID)

	if err := s.CacheRepository(repoID); err != nil {
		fail500(w, logContext, fmt.Errorf("failed to cache repository: %v", err))
		return
	}

	cmd, outPipe := utils.GitCommand(s.Config.GitPath, subCommand(service), "--stateless-rpc", r.RepoPath)
	defer outPipe.Close()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fail500(w, logContext, err)
		return
	}
	defer stdin.Close()

	if err := cmd.Start(); err != nil {
		fail500(w, logContext, err)
		return
	}
	defer utils.CleanUpProcessGroup(cmd)

	if _, err := io.Copy(stdin, body); err != nil {
		fail500(w, logContext, err)
		return
	}
	stdin.Close()

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-result", service))
	w.Header().Set("Cache-Control", "no-cache")

	if _, err := io.Copy(newWriteFlusher(w), outPipe); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if err := cmd.Wait(); err != nil {
		utils.LogError(logContext, err)
		return
	}

	if service == "git-receive-pack" {
		if err := s.handleGitReceivePack(w, r, repoID); err != nil {
			fail500(w, logContext, err)
		}
	}
}

func (s *Server) handleGitReceivePack(w http.ResponseWriter, r *Request, repoID uint64) error {
	const logContext = "handle-git-receive-pack"

	cmd := exec.Command(s.Config.GitPath, "gc")
	cmd.Dir = r.RepoPath
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "failed to run git gc")
	}

	packfileName, err := utils.GetPackfileName(r.RepoPath)
	if err != nil {
		return fmt.Errorf("failed to get packfile name: %w", err)
	}

	packfileInfo, err := os.Stat(packfileName)
	if err != nil {
		return fmt.Errorf("failed to get packfile info: %w", err)
	}

	repoResp, err := s.QueryService.GitopiaRepository(context.Background(), &gitopiatypes.QueryGetRepositoryRequest{Id: repoID})
	if err != nil {
		return fmt.Errorf("failed to get repository: %w", err)
	}

	var currentSize int64
	var previousCid string
	packfileResp, err := s.QueryService.GitopiaRepositoryPackfile(context.Background(), &storagetypes.QueryRepositoryPackfileRequest{RepositoryId: repoID})
	if err == nil && packfileResp != nil {
		currentSize = int64(packfileResp.Packfile.Size_)
		previousCid = packfileResp.Packfile.Cid
	}

	storageDelta := packfileInfo.Size() - currentSize

	userQuotaResp, err := s.QueryService.GitopiaUserQuota(context.Background(), &gitopiatypes.QueryUserQuotaRequest{Address: repoResp.Repository.Owner.Id})
	if err != nil {
		return fmt.Errorf("failed to get user quota: %w", err)
	}

	storageParams, err := s.QueryService.StorageParams(context.Background(), &storagetypes.QueryParamsRequest{})
	if err != nil {
		return fmt.Errorf("failed to get storage params: %w", err)
	}

	if !storageParams.Params.StoragePricePerMb.IsZero() {
		costInfo, err := utils.CalculateStorageCost(uint64(userQuotaResp.UserQuota.StorageUsed), uint64(storageDelta), storageParams.Params)
		if err != nil {
			return fmt.Errorf("failed to calculate storage cost: %w", err)
		}

		if !costInfo.StorageCharge.IsZero() {
			balance, err := s.QueryService.CosmosBankBalance(context.Background(), &banktypes.QueryBalanceRequest{
				Address: repoResp.Repository.Owner.Id,
				Denom:   costInfo.StorageCharge.Denom,
			})
			if err != nil {
				return fmt.Errorf("failed to get user balance: %w", err)
			}

			if balance.Balance.Amount.LT(costInfo.StorageCharge.Amount) {
				if err := os.RemoveAll(r.RepoPath); err != nil {
					return fmt.Errorf("failed to rollback local repository cache: %w", err)
				}
				return errors.New("insufficient balance for storage charge")
			}
		}
	}

	cid, err := utils.PinFile(s.IPFSClusterClient, packfileName)
	if err != nil {
		return fmt.Errorf("failed to pin packfile to IPFS cluster: %w", err)
	}

	log.WithFields(log.Fields{
		"operation":     "pin packfile",
		"repository_id": repoID,
		"packfile_name": filepath.Base(packfileName),
		"cid":           cid,
	}).Info("successfully pinned packfile")

	ipfsHTTPAPI, err := rpc.NewURLApiWithClient(fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")), &http.Client{})
	if err != nil {
		return fmt.Errorf("failed to create IPFS API: %w", err)
	}

	p, err := ipfspath.NewPath("/ipfs/" + cid)
	if err != nil {
		return fmt.Errorf("failed to create path: %w", err)
	}

	f, err := ipfsHTTPAPI.Unixfs().Get(context.Background(), p)
	if err != nil {
		return fmt.Errorf("failed to get packfile from IPFS: %w", err)
	}

	file, ok := f.(files.File)
	if !ok {
		return errors.New("invalid packfile format")
	}

	rootHash, err := merkleproof.ComputeMerkleRoot(file)
	if err != nil {
		return fmt.Errorf("failed to compute packfile merkle root: %w", err)
	}

	if err := s.GitopiaProxy.UpdateRepositoryPackfile(context.Background(), repoID, filepath.Base(packfileName), cid, rootHash, packfileInfo.Size(), previousCid); err != nil {
		return fmt.Errorf("failed to update repository packfile: %w", err)
	}

	if err := s.GitopiaProxy.PollForUpdate(context.Background(), func() (bool, error) {
		return s.GitopiaProxy.CheckPackfileUpdate(repoID, cid)
	}); err != nil {
		// If the packfile update was not confirmed, unpin the packfile from IPFS cluster
		if err := utils.UnpinFile(s.IPFSClusterClient, cid); err != nil {
			return fmt.Errorf("failed to unpin packfile from IPFS cluster: %w", err)
		}

		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timeout waiting for packfile update to be confirmed")
		}
		return fmt.Errorf("failed to verify packfile update: %w", err)
	}

	if packfileResp != nil && packfileResp.Packfile.Cid != "" {
		refCountResp, err := s.QueryService.StorageCidReferenceCount(context.Background(), &storagetypes.QueryCidReferenceCountRequest{Cid: packfileResp.Packfile.Cid})
		if err != nil {
			return fmt.Errorf("failed to get packfile reference count: %w", err)
		}

		if refCountResp.Count == 0 {
			if err := utils.UnpinFile(s.IPFSClusterClient, packfileResp.Packfile.Cid); err != nil {
				return fmt.Errorf("failed to unpin packfile from IPFS cluster: %w", err)
			}

			log.WithFields(log.Fields{
				"operation":     "unpin packfile",
				"repository_id": repoID,
				"packfile_name": filepath.Base(packfileName),
				"cid":           packfileResp.Packfile.Cid,
			}).Info("successfully unpinned packfile")
		}
	}

	log.WithFields(log.Fields{
		"operation":     "git push",
		"repository_id": repoID,
		"packfile_name": filepath.Base(packfileName),
		"cid":           cid,
	}).Info("git push completed")

	return nil
}
