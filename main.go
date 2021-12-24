package main

import (
	"bytes"
	"context"
	contextB "context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gitopia/git-server/utils"

	// "compress/gzip"

	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	git "github.com/gitopia/go-git/v5"
	"github.com/gitopia/go-git/v5/plumbing"
	"github.com/gitopia/go-git/v5/plumbing/cache"
	"github.com/gitopia/go-git/v5/plumbing/format/objfile"
	"github.com/gitopia/go-git/v5/plumbing/format/pktline"
	"github.com/gitopia/go-git/v5/plumbing/object"
	"github.com/gitopia/go-git/v5/plumbing/protocol/packp"
	"github.com/gitopia/go-git/v5/plumbing/revlist"
	"github.com/gitopia/go-git/v5/plumbing/transport/server"
	"github.com/gitopia/goar"
	"github.com/gitopia/goar/types"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/rs/cors"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	// "github.com/gitopia/go-git/v5/storage/memory"
	"github.com/gitopia/go-git/v5/plumbing/transport"
	"github.com/gitopia/go-git/v5/storage/filesystem"

	gitopiaTypes "github.com/gitopia/gitopia/x/gitopia/types"
)

const (
	branchPrefix = "refs/heads/"
	tagPrefix    = "refs/tags/"
)

type SaveToArweavePostBody struct {
	RepositoryID     uint64 `json:"repository_id"`
	RemoteRefName    string `json:"remote_ref_name"`
	NewRemoteRefSha  string `json:"new_remote_ref_sha"`
	PrevRemoteRefSha string `json:"prev_remote_ref_sha"`
}

func newWriteFlusher(w http.ResponseWriter) io.Writer {
	return writeFlusher{w.(interface {
		io.Writer
		http.Flusher
	})}
}

type writeFlusher struct {
	wf interface {
		io.Writer
		http.Flusher
	}
}

func (w writeFlusher) Write(p []byte) (int, error) {
	defer w.wf.Flush()
	return w.wf.Write(p)
}

type Credential struct {
	Username string
	Password string
}

func getCredential(req *http.Request) (Credential, error) {
	cred := Credential{}

	user, pass, ok := req.BasicAuth()
	if !ok {
		return cred, fmt.Errorf("authentication failed")
	}

	cred.Username = user
	cred.Password = pass

	return cred, nil
}

var reSlashDedup = regexp.MustCompile(`\/{2,}`)

func fail500(w http.ResponseWriter, context string, err error) {
	http.Error(w, "Internal server error", 500)
	logError(context, err)
}

func logError(context string, err error) {
	log.Printf("%s: %v\n", context, err)
}

func logInfo(context string, message string) {
	log.Printf("%s: %s\n", context, message)
}

func cleanUpProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}

	process := cmd.Process
	if process != nil && process.Pid > 0 {
		syscall.Kill(-process.Pid, syscall.SIGTERM)
	}

	go cmd.Wait()
}

func packLine(w io.Writer, s string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(s)+4, s)
	return err
}

func packFlush(w io.Writer) error {
	_, err := fmt.Fprint(w, "0000")
	return err
}

func subCommand(rpc string) string {
	return strings.TrimPrefix(rpc, "git-")
}

// Parse out namespace and repository name from the path.
// Examples:
// repo -> "", "repo"
// org/repo -> "org", "repo"
// org/suborg/rpeo -> "org/suborg", "repo"
func getNamespaceAndRepo(input string) (string, string) {
	if input == "" || input == "/" {
		return "", ""
	}

	// Remove duplicate slashes
	input = reSlashDedup.ReplaceAllString(input, "/")

	// Remove leading slash
	if input[0] == '/' && input != "/" {
		input = input[1:]
	}

	blocks := strings.Split(input, "/")
	num := len(blocks)

	if num < 2 {
		return "", blocks[0]
	}

	return strings.Join(blocks[0:num-1], "/"), blocks[num-1]
}

type Config struct {
	KeyDir     string       // Directory for server ssh keys. Only used in SSH strategy.
	Dir        string       // Directory that contains repositories
	GitPath    string       // Path to git binary
	GitUser    string       // User for ssh connections
	AutoCreate bool         // Automatically create repostories
	AutoHooks  bool         // Automatically setup git hooks
	Hooks      *HookScripts // Scripts for hooks/* directory
	Auth       bool         // Require authentication
}

// HookScripts represents all repository server-size git hooks
type HookScripts struct {
	PreReceive  string
	Update      string
	PostReceive string
}

// Configure hook scripts in the repo base directory
func (c *HookScripts) setupInDir(path string) error {
	basePath := filepath.Join(path, "hooks")
	scripts := map[string]string{
		"pre-receive":  c.PreReceive,
		"update":       c.Update,
		"post-receive": c.PostReceive,
	}

	// Cleanup any existing hooks first
	hookFiles, err := ioutil.ReadDir(basePath)
	if err == nil {
		for _, file := range hookFiles {
			if err := os.Remove(filepath.Join(basePath, file.Name())); err != nil {
				return err
			}
		}
	}

	// Write new hook files
	for name, script := range scripts {
		fullPath := filepath.Join(basePath, name)

		// Dont create hook if there's no script content
		if script == "" {
			continue
		}

		if err := ioutil.WriteFile(fullPath, []byte(script), 0755); err != nil {
			logError("hook-update", err)
			return err
		}
	}

	return nil
}

func (c *Config) KeyPath() string {
	return filepath.Join(c.KeyDir, "gitkit.rsa")
}

func (c *Config) Setup() error {
	if _, err := os.Stat(c.Dir); err != nil {
		if err = os.Mkdir(c.Dir, 0755); err != nil {
			return err
		}
	}

	if c.AutoHooks == true {
		return c.setupHooks()
	}

	return nil
}

func (c *Config) setupHooks() error {
	files, err := ioutil.ReadDir(c.Dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		path := filepath.Join(c.Dir, file.Name())

		if err := c.Hooks.setupInDir(path); err != nil {
			return err
		}
	}

	return nil
}

type service struct {
	method  string
	suffix  string
	handler func(string, http.ResponseWriter, *Request)
	rpc     string
}

type Server struct {
	config   Config
	services []service
	AuthFunc func(Credential, *Request) (bool, error)
}

type Request struct {
	*http.Request
	RepoName string
	RepoPath string
}

func New(cfg Config) *Server {
	s := Server{config: cfg}
	s.services = []service{
		{"GET", "/info/refs", s.getInfoRefs, ""},
		{"POST", "/git-upload-pack", s.postRPC, "git-upload-pack"},
		{"POST", "/git-receive-pack", s.postRPC, "git-receive-pack"},
	}

	// Use PATH if full path is not specified
	if s.config.GitPath == "" {
		s.config.GitPath = "git"
	}

	return &s
}

// findService returns a matching git subservice and parsed repository name
func (s *Server) findService(req *http.Request) (*service, string) {
	for _, svc := range s.services {
		if svc.method == req.Method && strings.HasSuffix(req.URL.Path, svc.suffix) {
			path := strings.Replace(req.URL.Path, svc.suffix, "", 1)
			return &svc, path
		}
	}
	return nil, ""
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logInfo("request", r.Method+" "+r.Host+r.URL.String())

	// Serve loose git objects
	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/objects") {
		defer r.Body.Close()

		blocks := strings.Split(r.URL.Path, "/")

		if len(blocks) != 4 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		repositoryId := blocks[2]
		objectHash := blocks[3]

		RepoPath := path.Join(s.config.Dir, fmt.Sprintf("%s.git", repositoryId))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		hash := plumbing.NewHash(objectHash)
		var obj plumbing.EncodedObject
		obj, err = repo.Storer.EncodedObject(plumbing.AnyObject, hash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		readCloser, err := obj.Reader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer readCloser.Close()

		objWriter := objfile.NewWriter(w)
		defer objWriter.Close()

		err = objWriter.WriteHeader(obj.Type(), obj.Size())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_, err = io.Copy(objWriter, readCloser)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		return
	}

	// Save objects to Arweave
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/save") {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body SaveToArweavePostBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		RepoPath := path.Join(s.config.Dir, fmt.Sprintf("%v.git", body.RepositoryID))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		prevHash := plumbing.NewHash(body.PrevRemoteRefSha)

		grpcUrl := viper.GetString("gitopia_grpc_url")
		grpcConn, err := grpc.Dial(grpcUrl,
			grpc.WithInsecure(),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer grpcConn.Close()

		queryClient := gitopiaTypes.NewQueryClient(grpcConn)

		var ignore []plumbing.Hash

		branchAllRes, err := queryClient.BranchAll(context.Background(), &gitopiaTypes.QueryGetAllBranchRequest{
			RepositoryId: body.RepositoryID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, branch := range branchAllRes.Branches {
			var hashes []plumbing.Hash

			if body.RemoteRefName != branchPrefix+branch.Name {
				hashes, err = revlist.Objects(repo.Storer, []plumbing.Hash{plumbing.NewHash(branch.Sha)}, nil)
			} else {
				if body.NewRemoteRefSha != branch.Sha {
					http.Error(w, errors.New("fatal: mismatch in remote ref sha").Error(), http.StatusBadRequest)
					return
				}
				if !prevHash.IsZero() { // new branch
					hashes, err = revlist.Objects(repo.Storer, []plumbing.Hash{prevHash}, nil)
				}
			}

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ignore = append(ignore, hashes...)
		}

		tagAllRes, err := queryClient.TagAll(context.Background(), &gitopiaTypes.QueryGetAllTagRequest{
			RepositoryId: body.RepositoryID,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, tag := range tagAllRes.Tags {
			var hashes []plumbing.Hash

			if body.RemoteRefName != tagPrefix+tag.Name {
				hashes, err = revlist.Objects(repo.Storer, []plumbing.Hash{plumbing.NewHash(tag.Sha)}, nil)
			} else {
				if body.NewRemoteRefSha != tag.Sha {
					http.Error(w, errors.New("fatal: mismatch in remote ref sha").Error(), http.StatusBadRequest)
					return
				}
				if !prevHash.IsZero() { // new tag
					hashes, err = revlist.Objects(repo.Storer, []plumbing.Hash{prevHash}, nil)
				}
			}

			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			ignore = append(ignore, hashes...)
		}

		hashes, err := revlist.Objects(repo.Storer, []plumbing.Hash{plumbing.NewHash(body.NewRemoteRefSha)}, ignore)

		// Initialize arweave client
		arweaveGatewayUrl := viper.GetString("arweave_gateway_url")
		wallet, err := goar.NewWalletFromPath("./test-keyfile.json", arweaveGatewayUrl)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, hash := range hashes {
			obj, err := repo.Storer.EncodedObject(plumbing.AnyObject, hash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			readCloser, err := obj.Reader()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer readCloser.Close()

			var buf bytes.Buffer
			objWriter := objfile.NewWriter(&buf)
			defer objWriter.Close()

			err = objWriter.WriteHeader(obj.Type(), obj.Size())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, err = io.Copy(objWriter, readCloser)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			repositoryId := strconv.FormatUint(body.RepositoryID, 10)

			// Upload object to Arweave
			_, err = wallet.SendData(
				buf.Bytes(),
				[]types.Tag{
					{
						Name:  "App-Name",
						Value: "gitopia",
					},
					{
						Name:  "App-Version",
						Value: "0.1.0",
					},
					{
						Name:  "Repository-Id",
						Value: repositoryId,
					},
					{
						Name:  "Object-Hash",
						Value: hash.String(),
					},
				},
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Mine Arweave block in testnet
		err = mineArweaveTestnetBlock()
		if err != nil {
			logError("error mining arweave testnet block", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}

		return
	}

	// Calculate commit diff
	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/diff") {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.DiffRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		RepoPath := path.Join(s.config.Dir, fmt.Sprintf("%d.git", body.RepositoryID))
		repo, err := git.PlainOpen(RepoPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if body.CommitSha == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		PreviousCommitHash := plumbing.NewHash(body.PreviousCommitSha)
		CommitHash := plumbing.NewHash(body.CommitSha)

		var previousCommit, commit *object.Commit

		if body.PreviousCommitSha == "" {
			previousCommit = nil
		} else {
			previousCommit, err = object.GetCommit(repo.Storer, PreviousCommitHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		commit, err = object.GetCommit(repo.Storer, CommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var previousTree, tree *object.Tree

		if previousCommit == nil {
			previousCommit, err = commit.Parent(0)
			if err != nil {
				previousTree = nil
			} else {
				previousTree, err = object.GetTree(repo.Storer, previousCommit.TreeHash)
				if err != nil {
					http.Error(w, err.Error(), http.StatusNotFound)
					return
				}
			}
		} else {
			previousTree, err = object.GetTree(repo.Storer, previousCommit.TreeHash)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		}

		tree, err = object.GetTree(repo.Storer, commit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var changes object.Changes
		changes, err = previousTree.Diff(tree)
		if err != nil {
			logError("commit-diff", fmt.Errorf("can't generate diff"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if body.OnlyStat {
			patch, err := changes.Patch()
			if err != nil {
				logError("commit-diff", fmt.Errorf("can't generate diff stats"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			addition := 0
			deletion := 0
			stats := patch.Stats()
			for _, l := range stats {
				addition += l.Addition
				deletion += l.Deletion
			}
			diffStat := utils.DiffStat{
				Addition: uint64(addition),
				Deletion: uint64(deletion),
			}
			DiffStatResponse := utils.DiffStatResponse{
				Stat:         diffStat,
				FilesChanged: uint64(changes.Len()),
			}
			DiffStatResponseJson, err := json.Marshal(DiffStatResponse)
			w.Write(DiffStatResponseJson)
			return
		}

		var diffs []*utils.Diff
		pageRes, err := utils.PaginateDiffResponse(changes, body.Pagination, 10, func(diff utils.Diff) error {
			diffs = append(diffs, &diff)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		diffResponse := utils.DiffResponse{
			Diff:       diffs,
			Pagination: pageRes,
		}
		diffResponseJson, err := json.Marshal(diffResponse)
		w.Write(diffResponseJson)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/diff") {
		defer r.Body.Close()

		decoder := json.NewDecoder(r.Body)
		var body utils.PullDiffRequestBody
		err := decoder.Decode(&body)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		headRepositoryPath := path.Join(s.config.Dir, fmt.Sprintf("%d.git", body.HeadRepositoryID))
		headRepository, err := git.PlainOpen(headRepositoryPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		baseRepositoryPath := path.Join(s.config.Dir, fmt.Sprintf("%d.git", body.BaseRepositoryID))
		baseRepository, err := git.PlainOpen(baseRepositoryPath)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}

		if body.HeadCommitSha == "" || body.BaseCommitSha == "" {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}

		headCommitHash := plumbing.NewHash(body.HeadCommitSha)
		baseCommitHash := plumbing.NewHash(body.BaseCommitSha)

		var headCommit, baseCommit *object.Commit

		headCommit, err = object.GetCommit(headRepository.Storer, headCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseCommit, err = object.GetCommit(baseRepository.Storer, baseCommitHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var headTree, baseTree *object.Tree

		headTree, err = object.GetTree(headRepository.Storer, headCommit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		baseTree, err = object.GetTree(baseRepository.Storer, baseCommit.TreeHash)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		var changes object.Changes
		changes, err = baseTree.Diff(headTree)
		if err != nil {
			logError("commit-diff", fmt.Errorf("can't generate diff"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if body.OnlyStat {
			patch, err := changes.Patch()
			if err != nil {
				logError("commit-diff", fmt.Errorf("can't generate diff stats"))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			addition := 0
			deletion := 0
			stats := patch.Stats()
			for _, l := range stats {
				addition += l.Addition
				deletion += l.Deletion
			}
			diffStat := utils.DiffStat{
				Addition: uint64(addition),
				Deletion: uint64(deletion),
			}
			DiffStatResponse := utils.DiffStatResponse{
				Stat:         diffStat,
				FilesChanged: uint64(changes.Len()),
			}
			DiffStatResponseJson, err := json.Marshal(DiffStatResponse)
			w.Write(DiffStatResponseJson)
			return
		}

		var diffs []*utils.Diff
		pageRes, err := utils.PaginateDiffResponse(changes, body.Pagination, 10, func(diff utils.Diff) error {
			diffs = append(diffs, &diff)
			return nil
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}

		diffResponse := utils.DiffResponse{
			Diff:       diffs,
			Pagination: pageRes,
		}
		diffResponseJson, err := json.Marshal(diffResponse)
		w.Write(diffResponseJson)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/upload") {
		defer r.Body.Close()

		uploadAttachmentHandler(w, r)
		return
	}

	if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/releases") {
		defer r.Body.Close()

		getAttachmentHandler(w, r)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/fork") {
		defer r.Body.Close()

		s.forkRepositoryHandler(w, r)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/commits") {
		defer r.Body.Close()

		s.pullRequestCommitsHandler(w, r)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/check") {
		defer r.Body.Close()

		s.pullRequestCheckHandler(w, r)
		return
	}

	if r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/pull/merge") {
		defer r.Body.Close()

		s.pullRequestMergeHandler(w, r)
		return
	}

	// Find the git subservice to handle the request
	svc, repoUrlPath := s.findService(r)
	if svc == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Determine namespace and repo name from request path
	repoNamespace, repoName := getNamespaceAndRepo(repoUrlPath)
	if repoName == "" {
		logError("auth", fmt.Errorf("no repo name provided"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	req := &Request{
		Request:  r,
		RepoName: path.Join(repoNamespace, repoName),
		RepoPath: path.Join(s.config.Dir, repoNamespace, repoName),
	}

	if s.config.Auth {
		if s.AuthFunc == nil {
			logError("auth", fmt.Errorf("no auth backend provided"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header()["WWW-Authenticate"] = []string{`Basic realm=""`}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		cred, err := getCredential(r)
		if err != nil {
			logError("auth", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		allow, err := s.AuthFunc(cred, req)
		if !allow || err != nil {
			if err != nil {
				logError("auth", err)
			}

			logError("auth", fmt.Errorf("rejected user %s", cred.Username))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	if !repoExists(req.RepoPath) && s.config.AutoCreate == true {
		err := initRepo(req.RepoName, &s.config)
		if err != nil {
			logError("repo-init", err)
		}
	}

	if !repoExists(req.RepoPath) {
		logError("repo-init", fmt.Errorf("%s does not exist", req.RepoPath))
		http.NotFound(w, r)
		return
	}

	svc.handler(svc.rpc, w, req)
}

func (s *Server) getInfoRefs(_ string, w http.ResponseWriter, r *Request) {
	context := "get-info-refs"
	rpc := r.URL.Query().Get("service")
	defer r.Body.Close()

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-advertisement", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	if !(rpc == "git-upload-pack" || rpc == "git-receive-pack") {
		http.Error(w, "Not Found", 404)
		return
	}
	fs := osfs.New(r.RepoPath)
	_, err := fs.Stat(".git")
	if err == nil {
		fs, err = fs.Chroot(".git")
		if err != nil {
			return
		}
	}
	storage := filesystem.NewStorageWithOptions(fs, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	ep, _ := transport.NewEndpoint(r.RepoPath)
	loader := server.MapLoader{}
	loader[ep.String()] = storage
	session := server.NewServer(loader)

	switch rpc {
	case "git-receive-pack":
		ts, err := session.NewReceivePackSession(ep, nil)
		if err != nil {
			logError(context, err)
		}
		defer ts.Close()
		adv, err := ts.AdvertisedReferences()
		if err != nil {
			logError(context, err)
		}

		// Enable smart protocol for http
		enc := pktline.NewEncoder(w)
		enc.Encode([]byte(fmt.Sprintf("# service=%s\n", rpc)))
		enc.Encode(nil)

		if err := adv.Encode(w); err != nil {
			logError(context, err)
			return
		}

	case "git-upload-pack":
		ts, err := session.NewReceivePackSession(ep, nil)
		if err != nil {
			logError(context, err)
		}
		defer ts.Close()
		adv, err := ts.AdvertisedReferences()
		if err != nil {
			logError(context, err)
		}

		// Enable smart protocol for http
		enc := pktline.NewEncoder(w)
		enc.Encode([]byte(fmt.Sprintf("# service=%s\n", rpc)))
		enc.Encode(nil)

		if err := adv.Encode(w); err != nil {
			logError(context, err)
			return
		}
	}

	// cmd, pipe := gitCommand(s.config.GitPath, subCommand(rpc), "--stateless-rpc", "--advertise-refs", r.RepoPath)
	// if err := cmd.Start(); err != nil {
	// 	fail500(w, context, err)
	// 	return
	// }
	// defer cleanUpProcessGroup(cmd)

	// if err := packLine(w, fmt.Sprintf("# service=%s\n", rpc)); err != nil {
	// 	logError(context, err)
	// 	return
	// }

	// if err := packFlush(w); err != nil {
	// 	logError(context, err)
	// 	return
	// }

}

// if err := cmd.Wait(); err != nil {
// 	logError(context, err)
// 	return
// }

func (s *Server) postRPC(rpc string, w http.ResponseWriter, r *Request) {
	// context := "post-rpc"
	// body := r.Body
	defer r.Body.Close()

	// if r.Header.Get("Content-Encoding") == "gzip" {
	// 	var err error
	// 	_, err := gzip.NewReader(r.Body)
	// 	if err != nil {
	// 		fail500(w, context, err)
	// 		return
	// 	}
	// }
	fs := osfs.New(r.RepoPath)
	_, err := fs.Stat(".git")
	if err == nil {
		fs, err = fs.Chroot(".git")
		if err != nil {
			return
		}
	}
	storage := filesystem.NewStorageWithOptions(fs, cache.NewObjectLRUDefault(), filesystem.Options{KeepDescriptors: true})
	ep, _ := transport.NewEndpoint(r.RepoPath)
	loader := server.MapLoader{}
	loader[ep.String()] = storage
	session := server.NewServer(loader)

	w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-result", rpc))
	w.Header().Add("Cache-Control", "no-cache")
	w.WriteHeader(200)

	switch rpc {
	case "git-receive-pack":
		ts, err := session.NewReceivePackSession(ep, nil)
		if err != nil {
			fmt.Println(err)
		}
		defer ts.Close()

		req := packp.NewReferenceUpdateRequest()
		if err := req.Decode(r.Body); err != nil {
			return
		}

		status, err := ts.ReceivePack(contextB.TODO(), req)
		if status != nil {
			if err := status.Encode(w); err != nil {
				fmt.Println(err)
			}
		}

		if err != nil {
			fmt.Println(err)
		}

	case "git-upload-pack":
		ts, err := session.NewUploadPackSession(ep, nil)
		if err != nil {
			fmt.Println(err)
		}
		defer ts.Close()

		req := packp.NewUploadPackRequest()
		if err := req.Decode(r.Body); err != nil {
			return
		}

		status, err := ts.UploadPack(contextB.TODO(), req)
		if status != nil {
			if err := status.Encode(w); err != nil {
				fmt.Println(err)
			}
		}

		if err != nil {
			fmt.Println(err)
		}
	}

}

func (s *Server) Setup() error {
	return s.config.Setup()
}

func initRepo(name string, config *Config) error {
	fullPath := path.Join(config.Dir, name)

	if err := exec.Command(config.GitPath, "init", "--bare", fullPath).Run(); err != nil {
		return err
	}

	if config.AutoHooks && config.Hooks != nil {
		return config.Hooks.setupInDir(fullPath)
	}

	return nil
}

func repoExists(p string) bool {
	_, err := os.Stat(path.Join(p, "objects"))
	return err == nil
}

func gitCommand(name string, args ...string) (*exec.Cmd, io.Reader) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = os.Environ()

	r, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout

	return cmd, r
}

func main() {

	viper.AddConfigPath(".")
	if os.Getenv("ENV") == "PRODUCTION" {
		viper.SetConfigName("config")
	} else {
		viper.SetConfigName("devconfig")
	}
	err := viper.ReadInConfig()
	if err != nil {
		log.Fatal(err)
	}

	// Configure git service
	service := New(Config{
		Dir:        viper.GetString("git_dir"),
		AutoCreate: true,
	})

	// Configure git server. Will create git repos path if it does not exist.
	// If hooks are set, it will also update all repos with new version of hook scripts.
	if err = service.Setup(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	mux.Handle("/", service)
	handler := cors.Default().Handler(mux)

	// Start HTTP server
	if err := http.ListenAndServe(":5000", handler); err != nil {
		log.Fatal(err)
	}
}
