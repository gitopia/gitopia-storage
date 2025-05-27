package handler

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/gitopia/git-server/utils"
	"github.com/gitopia/gitopia/v6/x/gitopia/types"
	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func getBranch(queryClient types.QueryClient, id, repoName, branchName string) (types.Branch, error) {
	res, err := queryClient.RepositoryBranch(context.Background(), &types.QueryGetRepositoryBranchRequest{
		Id:             id,
		RepositoryName: repoName,
		BranchName:     branchName,
	})
	if err != nil {
		return types.Branch{}, err
	}

	return res.Branch, nil
}

func branchExists(queryClient types.QueryClient, id, repoName, branchName string) bool {
	if _, err := getBranch(queryClient, id, repoName, branchName); err == nil {
		return true
	}

	return false
}

func getBranchNameAndTreePathFromPath(queryClient types.QueryClient, id, repoName string, parts []string) (string, string) {
	branchName := ""
	for i, part := range parts {
		branchName = strings.TrimPrefix(branchName+"/"+part, "/")
		if branchExists(queryClient, id, repoName, branchName) {
			treePath := strings.Join(parts[i+1:], "/")
			return branchName, treePath
		}
	}
	return "", ""
}

func GetRawFileHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("request: %s\n", r.Method+" "+r.Host+r.URL.String())

	if r.Method == "GET" {
		defer r.Body.Close()

		grpcUrl := viper.GetString("GITOPIA_ADDR")
		grpcConn, err := grpc.Dial(grpcUrl,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(nil).GRPCCodec())),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer grpcConn.Close()

		queryClient := types.NewQueryClient(grpcConn)

		blocks := strings.Split(r.URL.Path, "/")

		id := blocks[2]
		repoName := blocks[3]

		branchName, treePath := getBranchNameAndTreePathFromPath(queryClient, id, repoName, blocks[4:])
		if len(branchName) == 0 || len(treePath) == 0 {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		branch, err := getBranch(queryClient, id, repoName, branchName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// check if repository is cached
		cacheDir := viper.GetString("GIT_DIR")
		isCached, err := utils.IsRepoCached(branch.RepositoryId, cacheDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !isCached {
			err = utils.DownloadRepo(branch.RepositoryId, cacheDir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		repoPath := path.Join(cacheDir, fmt.Sprintf("%v.git", branch.RepositoryId))
		repo, err := git.PlainOpen(repoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		commitHash := plumbing.NewHash(branch.Sha)

		commit, err := object.GetCommit(repo.Storer, commitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		tree, err := object.GetTree(repo.Storer, commit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		treeEntry, err := tree.FindEntry(treePath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if treeEntry.Mode.IsFile() {
			blob, err := object.GetBlob(repo.Storer, treeEntry.Hash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}

			reader, err := blob.Reader()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			b, err := ioutil.ReadAll(reader)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write(b)
			return
		}

		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
	return
}
