package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/gitopia/git-server/app"
	"github.com/gitopia/git-server/pkg/merkleproof"
	"github.com/gitopia/git-server/utils"
	gc "github.com/gitopia/gitopia-go"
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

func main() {
	rootCmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate existing repositories and releases to IPFS",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			// Get all repositories from GIT_DIR
			gitDir := viper.GetString("GIT_DIR")
			entries, err := os.ReadDir(gitDir)
			if err != nil {
				return errors.Wrap(err, "failed to read GIT_DIR")
			}

			for _, entry := range entries {
				if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".git") {
					continue
				}

				// Extract repository ID from directory name
				repoIdStr := strings.TrimSuffix(entry.Name(), ".git")
				repoId, err := strconv.ParseUint(repoIdStr, 10, 64)
				if err != nil {
					fmt.Printf("Skipping invalid repository ID: %s\n", repoIdStr)
					continue
				}

				fmt.Printf("Processing repository %d\n", repoId)

				// git gc
				cmd := exec.Command("git", "gc")
				cmd.Dir = filepath.Join(gitDir, entry.Name())
				if err := cmd.Run(); err != nil {
					fmt.Printf("Error running git gc for repo %d: %v\n", repoId, err)
					continue
				}

				// Get packfile path
				repoPath := filepath.Join(gitDir, entry.Name())
				packfileName, err := utils.GetPackfileName(repoPath)
				if err != nil {
					fmt.Printf("Error getting packfile for repo %d: %v\n", repoId, err)
					continue
				}

				// Pin packfile to IPFS cluster
				cid, err := utils.PinFile(ipfsClusterClient, packfileName)
				if err != nil {
					fmt.Printf("Error pinning packfile for repo %d: %v\n", repoId, err)
					continue
				}

				// Get packfile from IPFS and calculate merkle root
				p, err := ipfspath.NewPath("/ipfs/" + cid)
				if err != nil {
					fmt.Printf("Error creating IPFS path for repo %d: %v\n", repoId, err)
					continue
				}

				f, err := ipfsHttpApi.Unixfs().Get(ctx, p)
				if err != nil {
					fmt.Printf("Error getting packfile from IPFS for repo %d: %v\n", repoId, err)
					continue
				}

				file, ok := f.(files.File)
				if !ok {
					fmt.Printf("Invalid packfile format for repo %d\n", repoId)
					continue
				}

				rootHash, err := merkleproof.ComputePackfileMerkleRoot(file, 256*1024)
				if err != nil {
					fmt.Printf("Error computing merkle root for repo %d: %v\n", repoId, err)
					continue
				}

				// Get packfile size
				packfileInfo, err := os.Stat(packfileName)
				if err != nil {
					fmt.Printf("Error getting packfile size for repo %d: %v\n", repoId, err)
					continue
				}

				// Update repository packfile on chain
				err = gitopiaProxy.UpdateRepositoryPackfile(
					ctx,
					repoId,
					filepath.Base(packfileName),
					cid,
					rootHash,
					packfileInfo.Size(),
				)
				if err != nil {
					fmt.Printf("Error updating repository packfile for repo %d: %v\n", repoId, err)
					continue
				}

				fmt.Printf("Successfully migrated repository %d\n", repoId)

				// Process releases for this repository
				var nextKey []byte
				for {
					releases, err := gitopiaClient.QueryClient().Gitopia.RepositoryReleaseAll(ctx, &gitopiatypes.QueryAllRepositoryReleaseRequest{
						Id: strconv.FormatUint(repoId, 10),
						Pagination: &query.PageRequest{
							Key:   nextKey,
							Limit: 100, // Process 100 releases at a time
						},
					})
					if err != nil {
						fmt.Printf("Error getting releases for repo %d: %v\n", repoId, err)
						break
					}

					for _, release := range releases.Release {
						fmt.Printf("Processing release %s for repository %d\n", release.TagName, repoId)

						for _, attachment := range release.Attachments {
							// Get attachment file path
							attachmentDir := viper.GetString("ATTACHMENT_DIR")
							filePath := filepath.Join(attachmentDir, attachment.Sha)

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

							_, err := ipfsClusterClient.Pin(ctx, attachmentCid, pinOpts)
							if err != nil {
								fmt.Printf("Error pinning attachment: %v\n", err)
								continue
							}

							// Open the file for merkle root calculation
							file, err := os.Open(filePath)
							if err != nil {
								fmt.Printf("Error opening attachment file: %v\n", err)
								continue
							}
							defer file.Close()

							// Create a files.File from the os.File
							ipfsFile := files.NewReaderFile(file)

							// Calculate merkle root
							rootHash, err := merkleproof.ComputePackfileMerkleRoot(ipfsFile, 256*1024)
							if err != nil {
								fmt.Printf("Error computing merkle root for attachment: %v\n", err)
								continue
							}

							// Get file size
							fileInfo, err := file.Stat()
							if err != nil {
								fmt.Printf("Error getting file size: %v\n", err)
								continue
							}

							// Update release asset on chain
							err = gitopiaProxy.UpdateReleaseAsset(
								ctx,
								repoId,
								release.TagName,
								attachment.Name,
								attachmentCid.String(),
								rootHash,
								fileInfo.Size(),
							)
							if err != nil {
								fmt.Printf("Error updating release asset: %v\n", err)
								continue
							}

							fmt.Printf("Successfully migrated attachment %s for release %s\n", attachment.Name, release.TagName)
						}
					}

					// Check if there are more pages
					if releases.Pagination == nil || len(releases.Pagination.NextKey) == 0 {
						break
					}
					nextKey = releases.Pagination.NextKey
				}
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

	// Bind flags to viper
	viper.BindPFlag("GIT_DIR", rootCmd.Flags().Lookup("git-dir"))
	viper.BindPFlag("ATTACHMENT_DIR", rootCmd.Flags().Lookup("attachment-dir"))
	viper.BindPFlag("IPFS_CLUSTER_PEER_HOST", rootCmd.Flags().Lookup("ipfs-cluster-peer-host"))
	viper.BindPFlag("IPFS_CLUSTER_PEER_PORT", rootCmd.Flags().Lookup("ipfs-cluster-peer-port"))
	viper.BindPFlag("IPFS_HOST", rootCmd.Flags().Lookup("ipfs-host"))
	viper.BindPFlag("IPFS_PORT", rootCmd.Flags().Lookup("ipfs-port"))

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
