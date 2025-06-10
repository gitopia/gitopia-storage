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
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	gc "github.com/gitopia/gitopia-go"
	"github.com/gitopia/gitopia-go/logger"
	"github.com/gitopia/gitopia-storage/app"
	"github.com/gitopia/gitopia-storage/pkg/merkleproof"
	"github.com/gitopia/gitopia-storage/utils"
	gitopiatypes "github.com/gitopia/gitopia/v6/x/gitopia/types"
	"github.com/ipfs-cluster/ipfs-cluster/api"
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
		return err
	}

	return os.WriteFile(ProgressFile, data, 0644)
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate existing repositories and releases to IPFS",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			gc.WithAppName(AppName)
			gc.WithChainId(viper.GetString("CHAIN_ID"))
			gc.WithGasPrices(viper.GetString("GAS_PRICES"))
			gc.WithGitopiaAddr(viper.GetString("GITOPIA_ADDR"))
			gc.WithTmAddr(viper.GetString("TM_ADDR"))
			gc.WithWorkingDir(viper.GetString("WORKING_DIR"))

			gitopiaClient, err := gc.NewClient(ctx, clientCtx, txf)
			if err != nil {
				return err
			}
			gitopiaProxy := app.NewGitopiaProxy(gitopiaClient)

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

			gitDir := viper.GetString("GIT_REPOS_DIR")

			// Process repositories
			var processedCount int
			var totalRepositories uint64
			nextKey := progress.RepositoryNextKey
			resumeFromFailed := progress.LastFailedRepo > 0

			for {
				repositories, err := gitopiaClient.QueryClient().Gitopia.RepositoryAll(ctx, &gitopiatypes.QueryAllRepositoryRequest{
					Pagination: &query.PageRequest{
						Key:   nextKey,
						Limit: 100, // Process 100 repositories at a time
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
					cmd := exec.Command("git", "clone", remoteUrl, repoDir)
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
					if strings.TrimSpace(string(output)) != branch.Branch.Name {
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

					rootHash, err := merkleproof.ComputePackfileMerkleRoot(file, 256*1024)
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
					)
					if err != nil {
						progress.FailedRepos[repository.Id] = err.Error()
						progress.LastFailedRepo = repository.Id
						if err := saveProgress(progress); err != nil {
							return errors.Wrap(err, "failed to save progress")
						}
						return errors.Wrapf(err, "error updating repository packfile for repo %d", repository.Id)
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
						Key:   nextKey,
						Limit: 100, // Process 100 releases at a time
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

					for _, attachment := range release.Attachments {
						attachmentDir := viper.GetString("ATTACHMENT_DIR")

						// Download release asset
						attachmentUrl := fmt.Sprintf("%s/releases/%s/%s/%s/%s",
							viper.GetString("GITOPIA_SERVER"),
							repository.Repository.Owner.Id,
							repository.Repository.Name,
							release.TagName,
							attachment.Name)
						filePath := filepath.Join(attachmentDir, attachment.Sha)
						cmd := exec.Command("wget", attachmentUrl, "-O", filePath)
						if err := cmd.Run(); err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error downloading release asset")
						}

						// verify sha256
						cmd = exec.Command("sha256sum", filePath)
						output, err := cmd.Output()
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error verifying sha256 for attachment")
						}
						if strings.TrimSpace(string(output)) != attachment.Sha {
							err := errors.Errorf("SHA256 mismatch for attachment %s: %s != %s", attachment.Name, strings.TrimSpace(string(output)), attachment.Sha)
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return err
						}

						// Pin attachment to IPFS cluster
						paths := []string{filePath}
						addParams := api.DefaultAddParams()
						addParams.Recursive = false
						addParams.Layout = "balanced"

						outputChan := make(chan api.AddedOutput)
						var attachmentCid api.Cid

						go func() {
							err := ipfsClusterClient.Add(ctx, paths, addParams, outputChan)
							if err != nil {
								fmt.Printf("Error adding attachment to IPFS cluster: %v\n", err)
								close(outputChan)
							}
						}()

						// Get CID from output channel
						for output := range outputChan {
							attachmentCid = output.Cid
						}

						// Pin the file with default options
						pinOpts := api.PinOptions{
							ReplicationFactorMin: -1,
							ReplicationFactorMax: -1,
							Name:                 attachment.Name,
						}

						_, err = ipfsClusterClient.Pin(ctx, attachmentCid, pinOpts)
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
						rootHash, err := merkleproof.ComputePackfileMerkleRoot(ipfsFile, 256*1024)
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

						// Update release asset on chain
						err = gitopiaProxy.UpdateReleaseAsset(
							ctx,
							release.RepositoryId,
							release.TagName,
							attachment.Name,
							attachmentCid.String(),
							rootHash,
							fileInfo.Size(),
							attachment.Sha,
						)
						if err != nil {
							progress.FailedReleases[release.Id] = err.Error()
							progress.LastFailedRelease = release.Id
							if err := saveProgress(progress); err != nil {
								return errors.Wrap(err, "failed to save progress")
							}
							return errors.Wrap(err, "error updating release asset")
						}

						fmt.Printf("Successfully migrated attachment %s for release %s\n", attachment.Name, release.TagName)
					}

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
	rootCmd.Flags().String("git-dir", "", "Directory containing git repositories")
	rootCmd.Flags().String("attachment-dir", "", "Directory containing release attachments")
	rootCmd.Flags().String("ipfs-cluster-peer-host", "", "IPFS cluster peer host")
	rootCmd.Flags().String("ipfs-cluster-peer-port", "", "IPFS cluster peer port")
	rootCmd.Flags().String("ipfs-host", "", "IPFS host")
	rootCmd.Flags().String("ipfs-port", "", "IPFS port")
	rootCmd.Flags().String("from", "", "Name or address of private key with which to sign")
	rootCmd.Flags().String("keyring-backend", "", "Select keyring's backend (os|file|kwallet|pass|test|memory)")
	rootCmd.Flags().String("fees", "", "Fees to pay along with transaction; eg: 10ulore")
	rootCmd.Flags().String("chain-id", "", "Chain ID")
	rootCmd.Flags().String("gas-prices", "", "Gas prices")
	rootCmd.Flags().String("gitopia-addr", "", "Gitopia address")
	rootCmd.Flags().String("tm-addr", "", "Tendermint address")
	rootCmd.Flags().String("working-dir", "", "Working directory")
	rootCmd.Flags().String("gitopia-server", "https://server.gitopia.com", "Gitopia server URL")

	// Bind flags to viper
	viper.BindPFlag("GIT_REPOS_DIR", rootCmd.Flags().Lookup("git-dir"))
	viper.BindPFlag("ATTACHMENT_DIR", rootCmd.Flags().Lookup("attachment-dir"))
	viper.BindPFlag("IPFS_CLUSTER_PEER_HOST", rootCmd.Flags().Lookup("ipfs-cluster-peer-host"))
	viper.BindPFlag("IPFS_CLUSTER_PEER_PORT", rootCmd.Flags().Lookup("ipfs-cluster-peer-port"))
	viper.BindPFlag("IPFS_HOST", rootCmd.Flags().Lookup("ipfs-host"))
	viper.BindPFlag("IPFS_PORT", rootCmd.Flags().Lookup("ipfs-port"))
	viper.BindPFlag("CHAIN_ID", rootCmd.Flags().Lookup("chain-id"))
	viper.BindPFlag("GAS_PRICES", rootCmd.Flags().Lookup("gas-prices"))
	viper.BindPFlag("GITOPIA_ADDR", rootCmd.Flags().Lookup("gitopia-addr"))
	viper.BindPFlag("TM_ADDR", rootCmd.Flags().Lookup("tm-addr"))
	viper.BindPFlag("WORKING_DIR", rootCmd.Flags().Lookup("working-dir"))
	viper.BindPFlag("GITOPIA_SERVER", rootCmd.Flags().Lookup("gitopia-server"))

	conf := sdk.GetConfig()
	conf.SetBech32PrefixForAccount(AccountAddressPrefix, AccountPubKeyPrefix)

	// Initialize context with logger
	ctx := logger.InitLogger(context.Background(), AppName)
	ctx = context.WithValue(ctx, client.ClientContextKey, &client.Context{})

	logger.FromContext(ctx).SetOutput(os.Stdout)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
