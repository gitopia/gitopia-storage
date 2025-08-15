package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	gc "github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
	ipfsclusterclient "github.com/ipfs-cluster/ipfs-cluster/api/rest/client"
	"github.com/ipfs/boxo/files"
	ipfspath "github.com/ipfs/boxo/path"
	"github.com/ipfs/kubo/client/rpc"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	AccountAddressPrefix = "gitopia"
	AccountPubKeyPrefix  = AccountAddressPrefix + sdk.PrefixPublic
	AppName              = "migrate"
	ProgressFile         = "migration_progress.json"
)

type MigrationProgress struct {
	RepositoryNextKey []byte            `json:"repository_next_key"`
	ReleaseNextKey    []byte            `json:"release_next_key"`
	FailedRepos       map[uint64]string `json:"failed_repos"`    // map[repoID]error
	FailedReleases    map[uint64]string `json:"failed_releases"` // map[releaseID]error
	LastFailedRepo    uint64            `json:"last_failed_repo"`
	LastFailedRelease uint64            `json:"last_failed_release"`
}

func loadProgress() (*MigrationProgress, error) {
	if _, err := os.Stat(ProgressFile); os.IsNotExist(err) {
		return &MigrationProgress{
			FailedRepos:    make(map[uint64]string),
			FailedReleases: make(map[uint64]string),
		}, nil
	}

	data, err := os.ReadFile(ProgressFile)
	if err != nil {
		return nil, err
	}

	var progress MigrationProgress
	if err := json.Unmarshal(data, &progress); err != nil {
		return nil, err
	}

	// Initialize maps if they're nil
	if progress.FailedRepos == nil {
		progress.FailedRepos = make(map[uint64]string)
	}
	if progress.FailedReleases == nil {
		progress.FailedReleases = make(map[uint64]string)
	}

	return &progress, nil
}

func saveProgress(progress *MigrationProgress) error {
	data, err := json.Marshal(progress)
	if err != nil {
		return errors.Wrap(err, "failed to marshal progress")
	}

	return os.WriteFile(ProgressFile, data, 0644)
}

// processLFSObjects handles LFS objects for a specific repository
func processLFSObjects(ctx context.Context, repositoryId uint64, repoDir string, ipfsClusterClient ipfsclusterclient.Client, ipfsHttpApi *rpc.HttpApi, gitopiaProxy *app.GitopiaProxy) error {
	// Get LFS objects directory
	lfsObjectsDir := filepath.Join(repoDir, "lfs", "objects")

	// Check if LFS objects directory exists
	if _, err := os.Stat(lfsObjectsDir); os.IsNotExist(err) {
		fmt.Printf("No LFS objects directory found for repository %d\n", repositoryId)
		return nil
	}

	// Walk through LFS objects directory
	var lfsObjects []string
	err := filepath.Walk(lfsObjectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-regular files
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}

		// Get relative path from lfs/objects directory
		relPath, err := filepath.Rel(lfsObjectsDir, path)
		if err != nil {
			return err
		}

		// LFS objects are stored in subdirectories like 0b/78/0b7810a9fef2b44e9381e7f4e8ffd699a71e359931ab236bcc356ab8fd2a4575
		// The filename itself is the complete OID
		pathParts := strings.Split(relPath, string(filepath.Separator))
		if len(pathParts) >= 3 {
			// Extract the filename (last part) which is the complete OID
			oid := pathParts[len(pathParts)-1]
			// Validate OID format (64 hex characters)
			if len(oid) == 64 {
				lfsObjects = append(lfsObjects, oid)
			}
		}

		return nil
	})

	if err != nil {
		return errors.Wrap(err, "failed to walk LFS objects directory")
	}

	if len(lfsObjects) == 0 {
		fmt.Printf("No LFS objects found for repository %d\n", repositoryId)
		return nil
	}

	fmt.Printf("Found %d LFS objects for repository %d\n", len(lfsObjects), repositoryId)

	// Process each LFS object
	for i, oid := range lfsObjects {
		fmt.Printf("Processing LFS object %d/%d: %s\n", i+1, len(lfsObjects), oid)

		// Get the actual file path
		oidPath := filepath.Join(lfsObjectsDir, oid[:2], oid[2:])

		// Pin LFS object to IPFS cluster
		cid, err := utils.PinFile(ipfsClusterClient, oidPath)
		if err != nil {
			return errors.Wrapf(err, "error pinning LFS object %s", oid)
		}

		// Get LFS object from IPFS and calculate merkle root
		p, err := ipfspath.NewPath("/ipfs/" + cid)
		if err != nil {
			return errors.Wrapf(err, "error creating IPFS path for LFS object %s", oid)
		}

		f, err := ipfsHttpApi.Unixfs().Get(ctx, p)
		if err != nil {
			return errors.Wrapf(err, "error getting LFS object from IPFS: %s", oid)
		}

		file, ok := f.(files.File)
		if !ok {
			return errors.Errorf("invalid LFS object format: %s", oid)
		}

		rootHash, err := merkleproof.ComputeMerkleRoot(file)
		if err != nil {
			return errors.Wrapf(err, "error computing merkle root for LFS object %s", oid)
		}

		// Get LFS object size
		lfsObjectInfo, err := os.Stat(oidPath)
		if err != nil {
			return errors.Wrapf(err, "error getting LFS object size: %s", oid)
		}

		// Update LFS object on chain
		err = gitopiaProxy.UpdateLFSObject(
			ctx,
			repositoryId,
			oid,
			cid,
			rootHash,
			lfsObjectInfo.Size(),
		)
		if err != nil {
			return errors.Wrapf(err, "error updating LFS object %s for repo %d", oid, repositoryId)
		}

		// Poll to check if the LFS object was updated
		fmt.Printf("Verifying LFS object update for oid %s...\n", oid)
		err = gitopiaProxy.PollForUpdate(ctx, func() (bool, error) {
			lfsObject, err := gitopiaProxy.LFSObjectByRepositoryIdAndOid(ctx, repositoryId, oid)
			if err != nil {
				return false, err
			}

			return lfsObject.Cid == cid, nil
		})
		if err != nil {
			return errors.Wrapf(err, "failed to verify LFS object update for oid %s", oid)
		}

		fmt.Printf("Successfully processed LFS object %s (CID: %s)\n", oid, cid)
	}

	return nil
}

func main() {
	rootCmd := &cobra.Command{
		Use:               "migrate",
		Short:             "Migrate existing repositories and releases to IPFS",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return gc.CommandInit(cmd, AppName)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create required directories if they don't exist
			gitDir := viper.GetString("GIT_REPOS_DIR")
			if err := os.MkdirAll(gitDir, 0755); err != nil {
				return errors.Wrap(err, "failed to create git repositories directory")
			}

			attachmentDir := viper.GetString("ATTACHMENT_DIR")
			if err := os.MkdirAll(attachmentDir, 0755); err != nil {
				return errors.Wrap(err, "failed to create attachments directory")
			}

			// Load progress
			progress, err := loadProgress()
			if err != nil {
				return errors.Wrap(err, "failed to load progress")
			}

			// Initialize Gitopia client
			ctx := cmd.Context()
			clientCtx := client.GetClientContextFromCmd(cmd)
			txf, err := tx.NewFactoryCLI(clientCtx, cmd.Flags())
			if err != nil {
				return errors.Wrap(err, "error initializing tx factory")
			}
			txf = txf.WithGasAdjustment(app.GAS_ADJUSTMENT)

			gitopiaClient, err := gc.NewClient(ctx, clientCtx, txf)
			if err != nil {
				return err
			}
			defer gitopiaClient.Close()

			batchTxManager := app.NewBatchTxManager(gitopiaClient, app.BLOCK_TIME)
			batchTxManager.Start()
			defer batchTxManager.Stop()

			gitopiaProxy := app.NewGitopiaProxy(gitopiaClient, batchTxManager)

			// Initialize IPFS cluster client
			ipfsCfg := &ipfsclusterclient.Config{
				Host:    viper.GetString("IPFS_CLUSTER_PEER_HOST"),
				Port:    viper.GetString("IPFS_CLUSTER_PEER_PORT"),
				Timeout: time.Minute * 5,
			}
			ipfsClusterClient, err := ipfsclusterclient.NewDefaultClient(ipfsCfg)
			if err != nil {
				return errors.Wrap(err, "failed to create IPFS cluster client")
			}

			// Initialize IPFS HTTP API client
			ipfsHttpApi, err := rpc.NewURLApiWithClient(
				fmt.Sprintf("http://%s:%s", viper.GetString("IPFS_HOST"), viper.GetString("IPFS_PORT")),
				&http.Client{},
			)
			if err != nil {
				return errors.Wrap(err, "failed to create IPFS API")
			}

			// Process repositories
			var processedCount int
			var totalRepositories uint64
			nextKey := progress.RepositoryNextKey
			resumeFromFailed := progress.LastFailedRepo > 0

			for {
				repositories, err := gitopiaClient.QueryClient().Gitopia.RepositoryAll(ctx, &gitopiatypes.QueryAllRepositoryRequest{
					Pagination: &query.PageRequest{
						Key: nextKey,
					},
				})
				if err != nil {
					return errors.Wrap(err, "failed to get repositories")
				}

				if totalRepositories == 0 {
					totalRepositories = repositories.Pagination.Total
					fmt.Printf("Total repositories to process: %d\n", totalRepositories)
				}

				for _, repository := range repositories.Repository {
					// If we're resuming from a failure, skip until we reach the failed repo
					if resumeFromFailed && repository.Id < progress.LastFailedRepo {
						processedCount++
						continue
					}
					resumeFromFailed = false // Reset the flag once we've found our position

					// Check repository is empty
					branch, err := gitopiaClient.QueryClient().Gitopia.RepositoryBranch(ctx, &gitopiatypes.QueryGetRepositoryBranchRequest{
						Id:             repository.Owner.Id,
						RepositoryName: repository.Name,
						BranchName:     repository.DefaultBranch,
					})
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error getting repository branches for repo %d", repository.Id)
					}
					if branch.Branch.Name == "" {
						fmt.Printf("Repository %d is empty, skipping\n", repository.Id)
						processedCount++
						continue
					}

					fmt.Printf("Processing repository %d (%d/%d)\n", repository.Id, processedCount+1, totalRepositories)

					// clone repository
					repoDir := filepath.Join(gitDir, fmt.Sprintf("%d.git", repository.Id))
					remoteUrl := fmt.Sprintf("gitopia://%s/%s", repository.Owner.Id, repository.Name)
					cmd := exec.Command("git", "clone", "--bare", remoteUrl, repoDir)
					if err := cmd.Run(); err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error cloning repository %d", repository.Id)
					}

					cmd = exec.Command("git", "gc")
					cmd.Dir = repoDir
					if err := cmd.Run(); err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error running git gc for repo %d", repository.Id)
					}

					// Check branch ref matches
					cmd = exec.Command("git", "rev-parse", branch.Branch.Name)
					cmd.Dir = repoDir
					output, err := cmd.Output()
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error checking branch ref for repo %d", repository.Id)
					}
					if strings.TrimSpace(string(output)) != branch.Branch.Sha {
						err := errors.Errorf("branch ref for repo %d does not match: %s", repository.Id, branch.Branch.Name)
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return err
					}

					// Get packfile path
					packfileName, err := utils.GetPackfileName(repoDir)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error getting packfile for repo %d", repository.Id)
					}

					// Pin packfile to IPFS cluster
					cid, err := utils.PinFile(ipfsClusterClient, packfileName)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error pinning packfile for repo %d", repository.Id)
					}

					// Get packfile from IPFS and calculate merkle root
					p, err := ipfspath.NewPath("/ipfs/" + cid)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error creating IPFS path for repo %d", repository.Id)
					}

					f, err := ipfsHttpApi.Unixfs().Get(ctx, p)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error getting packfile from IPFS for repo %d", repository.Id)
					}

					file, ok := f.(files.File)
					if !ok {
						err := errors.Errorf("invalid packfile format for repo %d", repository.Id)
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return err
					}

					rootHash, err := merkleproof.ComputeMerkleRoot(file)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error computing merkle root for repo %d", repository.Id)
					}

					// Get packfile size
					packfileInfo, err := os.Stat(packfileName)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error getting packfile size for repo %d", repository.Id)
					}

					// Update repository packfile on chain
					err = gitopiaProxy.UpdateRepositoryPackfile(
						ctx,
						repository.Id,
						filepath.Base(packfileName),
						cid,
						rootHash,
						packfileInfo.Size(),
						"",
					)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error updating repository packfile for repo %d", repository.Id)
					}

					// Poll to check if the packfile was updated
					fmt.Printf("Verifying packfile update for repository %d...\n", repository.Id)
					err = gitopiaProxy.PollForUpdate(ctx, func() (bool, error) {
						packfile, err := gitopiaProxy.RepositoryPackfile(ctx, repository.Id)
						if err != nil {
							return false, err
						}
						return packfile.Cid == cid, nil
					})
					if err != nil {
						err = errors.Wrapf(err, "failed to verify packfile update for repo %d", repository.Id)
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return err
					}
					fmt.Printf("Packfile update for repository %d verified.\n", repository.Id)

					// Handle LFS objects for osmosis-labs/osmosis repository
					if repository.Owner.Id == "gitopia1vp8p5xag26epvzs0ujx6m5x2enjy5p8qe3yrjysuenhn22wfu3ls4j2fyw" && repository.Name == "osmosis" {
						fmt.Printf("Processing LFS objects for repository %d (osmosis-labs/osmosis)\n", repository.Id)
						if err := processLFSObjects(ctx, repository.Id, repoDir, ipfsClusterClient, ipfsHttpApi, gitopiaProxy); err != nil {
							progress.FailedRepos[repository.Id] = err.Error()
							progress.LastFailedRepo = repository.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrapf(err, "error processing LFS objects for repo %d", repository.Id)
						}
						fmt.Printf("Successfully processed LFS objects for repository %d\n", repository.Id)
					}

					// Remove from failed repos if it was previously failed
					delete(progress.FailedRepos, repository.Id)
					progress.RepositoryNextKey = nextKey
					if err := saveProgress(progress); err != nil {
						return errors.Wrap(err, "failed to save progress")
					}

					processedCount++
					fmt.Printf("Successfully migrated repository %d\n", repository.Id)
				}

				if repositories.Pagination == nil || len(repositories.Pagination.NextKey) == 0 {
					break
				}
				nextKey = repositories.Pagination.NextKey
			}

			// Process releases
			processedCount = 0
			var totalReleases uint64
			nextKey = progress.ReleaseNextKey
			resumeFromFailed = progress.LastFailedRelease > 0

			for {
				releases, err := gitopiaClient.QueryClient().Gitopia.ReleaseAll(ctx, &gitopiatypes.QueryAllReleaseRequest{
					Pagination: &query.PageRequest{
						Key: nextKey,
					},
				})
				if err != nil {
					return errors.Wrap(err, "failed to get releases")
				}

				if totalReleases == 0 {
					totalReleases = releases.Pagination.Total
					fmt.Printf("Total releases to process: %d\n", totalReleases)
				}

				for _, release := range releases.Release {
					// If we're resuming from a failure, skip until we reach the failed release
					if resumeFromFailed && release.Id < progress.LastFailedRelease {
						processedCount++
						continue
					}
					resumeFromFailed = false // Reset the flag once we've found our position

					fmt.Printf("Processing release %s for repository %d (%d/%d)\n", release.TagName, release.RepositoryId, processedCount+1, totalReleases)

					// if there are no attachments, skip
					if len(release.Attachments) == 0 {
						processedCount++
						continue
					}

					// fetch repository
					repository, err := gitopiaClient.QueryClient().Gitopia.Repository(ctx, &gitopiatypes.QueryGetRepositoryRequest{
						Id: release.RepositoryId,
					})
					if err != nil {
						progress.FailedReleases[release.Id] = err.Error()
						progress.LastFailedRelease = release.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrap(err, "error getting repository")
					}

					assets := make([]*storagetypes.ReleaseAssetUpdate, 0)
					for _, attachment := range release.Attachments {
						attachmentDir := viper.GetString("ATTACHMENT_DIR")

						// Download release asset
						attachmentUrl := fmt.Sprintf("%s/releases/%s/%s/%s/%s",
							viper.GetString("GIT_SERVER_HOST"),
							repository.Repository.Owner.Id,
							repository.Repository.Name,
							release.TagName,
							attachment.Name)

						filePath := filepath.Join(attachmentDir, attachment.Sha)
						cmd := exec.Command("wget", attachmentUrl, "-O", filePath)
						output, err := cmd.CombinedOutput()
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrapf(err, "error downloading release asset: %s", string(output))
						}

						// verify sha256
						cmd = exec.Command("sha256sum", filePath)
						output, err = cmd.Output()
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error verifying sha256 for attachment")
						}
						// Extract just the hash part (first 64 characters before the space)
						calculatedHash := strings.Fields(string(output))[0]
						if calculatedHash != attachment.Sha {
							err := errors.Errorf("SHA256 mismatch for attachment %s: %s != %s", attachment.Name, calculatedHash, attachment.Sha)
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return err
						}

						cid, err := utils.PinFile(ipfsClusterClient, filePath)
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error pinning attachment")
						}

						// Open the file for merkle root calculation
						file, err := os.Open(filePath)
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error opening attachment file")
						}
						defer file.Close()

						stat, err := file.Stat()
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error getting file stat")
						}

						// Create a files.File from the os.File
						ipfsFile, err := files.NewReaderPathFile(filePath, file, stat)
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error creating files.File from attachment file")
						}

						// Calculate merkle root
						rootHash, err := merkleproof.ComputeMerkleRoot(ipfsFile)
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error computing merkle root for attachment")
						}

						// Get file size
						fileInfo, err := file.Stat()
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error getting file size")
						}

						// Collect release asset for batch update
						asset := &storagetypes.ReleaseAssetUpdate{
							Name:     attachment.Name,
							Cid:      cid,
							RootHash: rootHash,
							Size_:    uint64(fileInfo.Size()),
							Sha256:   attachment.Sha,
						}
						assets = append(assets, asset)

						fmt.Printf("Successfully migrated attachment %s for release %s\n", attachment.Name, release.TagName)
					}

					err = gitopiaProxy.UpdateReleaseAssets(ctx, release.RepositoryId, release.TagName, assets)
					if err != nil {
						progress.FailedReleases[release.Id] = err.Error()
						progress.LastFailedRelease = release.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrap(err, "error updating release assets")
					}

					// Poll to check if the release assets were updated
					fmt.Printf("Verifying release assets update for release %s...\n", release.TagName)
					err = gitopiaProxy.PollForUpdate(ctx, func() (bool, error) {
						updatedAssets, err := gitopiaProxy.RepositoryReleaseAssets(ctx, release.RepositoryId, release.TagName)
						if err != nil {
							return false, err
						}

						if len(updatedAssets) != len(assets) {
							return false, nil
						}

						// Create a map of expected assets for easy lookup
						expectedAssets := make(map[string]*storagetypes.ReleaseAssetUpdate)
						for _, asset := range assets {
							expectedAssets[asset.Name] = asset
						}

						for _, updatedAsset := range updatedAssets {
							expected := expectedAssets[updatedAsset.Name]
							if updatedAsset.Cid != expected.Cid {
								// CID mismatch
								return false, nil
							}
						}

						return true, nil
					})
					if err != nil {
						err = errors.Wrapf(err, "failed to verify release assets update for release %s", release.TagName)
						progress.FailedReleases[release.Id] = err.Error()
						progress.LastFailedRelease = release.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return err
					}
					fmt.Printf("Release assets update for release %s verified.\n", release.TagName)

					// Remove from failed releases if it was previously failed
					delete(progress.FailedReleases, release.Id)
					progress.ReleaseNextKey = nextKey
					if err := saveProgress(progress); err != nil {
						return errors.Wrap(err, "failed to save progress")
					}

					processedCount++
				}

				// Check if there are more pages
				if releases.Pagination == nil || len(releases.Pagination.NextKey) == 0 {
					break
				}
				nextKey = releases.Pagination.NextKey
			}

			return nil
		},
	}

	// Add flags
	rootCmd.Flags().String("from", "", "Name or address of private key with which to sign")
	rootCmd.Flags().String("keyring-backend", "", "Select keyring's backend (os|file|kwallet|pass|test|memory)")
	rootCmd.Flags().String("fees", "", "Fees to pay along with transaction; eg: 10ulore")

	conf := sdk.GetConfig()
	conf.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)

	// Initialize context with logger
	ctx := logger.InitLogger(context.Background(), AppName)
	ctx = context.WithValue(ctx, client.ClientContextKey, &client.Context{})

	logger.FromContext(ctx).SetOutput(os.Stdout)

	viper.SetConfigFile("config.toml")
	viper.ReadInConfig()

	gc.WithAppName(AppName)
	gc.WithChainId(viper.GetString("CHAIN_ID"))
	gc.WithGasPrices(viper.GetString("GAS_PRICES"))
	gc.WithGitopiaAddr(viper.GetString("GITOPIA_ADDR"))
	gc.WithTmAddr(viper.GetString("TM_ADDR"))
	gc.WithWorkingDir(viper.GetString("WORKING_DIR"))

	rootCmd.AddCommand(keys.Commands(viper.GetString("WORKING_DIR")))

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
