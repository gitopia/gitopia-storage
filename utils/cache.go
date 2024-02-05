package utils

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

const (
	cacheExpiryTime = 24 * time.Hour
)

func InitializeDB(dbPath string) *sql.DB {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatal(err)
	}

	// Create table if not exists
	createTableSQL := `CREATE TABLE IF NOT EXISTS cache (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
		repo_id INTEGER NOT NULL UNIQUE,
		parent_repo_id INTEGER,
        cid TEXT NOT NULL UNIQUE,
		packfile_name TEXT NOT NULL UNIQUE,
		creation_time DATETIME NOT NULL,
        last_accessed_time DATETIME NOT NULL,
        expiry_time DATETIME NOT NULL
    );`
	_, err = db.Exec(createTableSQL)
	if err != nil {
		log.Fatal(err)
	}

	return db
}

func DbExists(dbPath string) bool {
	_, err := os.Stat(dbPath)
	return !os.IsNotExist(err)
}

func InsertCacheData(db *sql.DB, repoId uint64, parentRepoId uint64, cid string, packfileName string, creationTime, lastAccessedTime, expiryTime time.Time) {
	insertSQL := `INSERT INTO cache (repo_id, parent_repo_id, cid, packfile_name, creation_time, last_accessed_time, expiry_time) VALUES (?, ?, ?, ?, ?, ?, ?)`
	statement, err := db.Prepare(insertSQL)
	if err != nil {
		log.Fatal(err)
	}
	_, err = statement.Exec(repoId, parentRepoId, cid, packfileName, creationTime, lastAccessedTime, expiryTime)
	if err != nil {
		log.Fatal(err)
	}
}

func IsCached(db *sql.DB, repoId uint64, cid string, packfileName string) bool {
	query := `SELECT COUNT(*) FROM cache WHERE repo_id = ? AND cid = ? AND packfile_name = ?`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(repoId, cid, packfileName).Scan(&count)
	if err != nil {
		log.Fatal(err)
	}

	return count > 0
}

func ReadCacheData(db *sql.DB) {
	row, err := db.Query("SELECT * FROM cache")
	if err != nil {
		log.Fatal(err)
	}
	defer row.Close()

	for row.Next() {
		var id int
		var cid, packfileName string
		var creationTime, lastAccessedTime, expiryTime time.Time
		row.Scan(&id, &cid, &packfileName, &creationTime, &lastAccessedTime, &expiryTime)
	}
}

func DownloadRepo(db *sql.DB, id uint64, cacheDir string, config *Config) error {
	queryClient, err := gitopia.GetQueryClient(viper.GetString("GITOPIA_ADDR"))
	if err != nil {
		return errors.Wrap(err, "error connecting to gitopia")
	}

	res, err := queryClient.Gitopia.Repository(context.Background(), &gitopiatypes.QueryGetRepositoryRequest{
		Id: id,
	})
	if err != nil {
		return err
	}

	// download parent repos first
	if res.Repository.Parent != 0 {
		err := DownloadRepo(db, res.Repository.Parent, cacheDir, config)
		if err != nil {
			return errors.Wrap(err, "error downloading parent repo")
		}
	}

	storageResp, err := queryClient.Gitopia.RepositoryStorage(context.Background(), &gitopiatypes.QueryGetRepositoryStorageRequest{
		RepositoryId: id,
	})
	if err != nil {
		return errors.Wrap(err, "storage not found")
	}

	// make sure dependent parent repos are there
	if err := downloadPackfile(storageResp.Storage.Latest.Id, storageResp.Storage.Latest.Name, cacheDir, config); err != nil {
		return errors.Wrap(err, "error downloading packfile")
	}

	// create refs on the server
	createBranchesAndTags(queryClient, res.Repository.Owner.Id, res.Repository.Name, cacheDir, config)

	InsertCacheData(db, id, res.Repository.Parent, storageResp.Storage.Latest.Id, storageResp.Storage.Latest.Name, time.Now(), time.Now(), time.Now().Add(cacheExpiryTime))

	return nil
}

func downloadPackfile(cid string, packfileName string, cacheDir string, config *Config) error {
	ipfsUrl := fmt.Sprintf("https://%s.%s/%s", cid, viper.GetString("IPFS_GATEWAY"), packfileName)

	resp, err := http.Get(ipfsUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	out, err := os.Create(cacheDir + "/objects/pack/" + packfileName)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	// Build pack index file
	packfilePath := fmt.Sprintf("objects/pack/%s", packfileName)
	cmd, outPipe := GitCommand(config.GitPath, "index-pack", packfilePath)
	cmd.Dir = cacheDir
	if err := cmd.Start(); err != nil {
		return err
	}
	defer CleanUpProcessGroup(cmd)

	_, err = io.Copy(io.Discard, outPipe)
	if err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func createBranchesAndTags(queryClient gitopia.Query, repoOwner string, repoName string, cacheDir string, config *Config) error {
	branchAllRes, err := queryClient.Gitopia.RepositoryBranchAll(context.Background(), &gitopiatypes.QueryAllRepositoryBranchRequest{
		Id:             repoOwner,
		RepositoryName: repoName,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}
	for _, branch := range branchAllRes.Branch {
		cmd, outPipe := GitCommand(config.GitPath, "branch", branch.Name, branch.Sha)
		cmd.Dir = cacheDir
		if err := cmd.Start(); err != nil {
			return err
		}
		defer CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			return err
		}

		if err := cmd.Wait(); err != nil {
			return err
		}
	}

	tagAllRes, err := queryClient.Gitopia.RepositoryTagAll(context.Background(), &gitopiatypes.QueryAllRepositoryTagRequest{
		Id:             repoOwner,
		RepositoryName: repoName,
		Pagination: &query.PageRequest{
			Limit: math.MaxUint64,
		},
	})
	if err != nil {
		return err
	}
	for _, tag := range tagAllRes.Tag {
		cmd, outPipe := GitCommand(config.GitPath, "tag", tag.Name, tag.Sha)
		cmd.Dir = cacheDir
		if err := cmd.Start(); err != nil {
			return err
		}
		defer CleanUpProcessGroup(cmd)

		_, err = io.Copy(io.Discard, outPipe)
		if err != nil {
			return err
		}

		if err := cmd.Wait(); err != nil {
			return err
		}
	}
	return nil
}

// UpdateCacheEntry updates the last_accessed_time and expiry_time for a cache entry.
func UpdateCacheEntry(db *sql.DB, repoID uint64, cid, packfileName string) error {
	newExpiryTime := time.Now().Add(cacheExpiryTime)

	updateSQL := `UPDATE cache
                  SET last_accessed_time = CURRENT_TIMESTAMP,
                      expiry_time = ?
                  WHERE repo_id = ? AND cid = ? AND packfile_name = ?;`

	_, err := db.Exec(updateSQL, newExpiryTime, repoID, cid, packfileName)
	if err != nil {
		return err
	}

	return nil
}

// CleanupCacheEntry deletes a cache entry matching the repo_id, cid, and packfile_name.
func CleanupCacheEntry(db *sql.DB, repoID uint64, cid, packfileName string) error {
	deleteSQL := `DELETE FROM cache WHERE repo_id = ? AND cid = ? AND packfile_name = ?;`

	_, err := db.Exec(deleteSQL, repoID, cid, packfileName)
	if err != nil {
		return err
	}

	return nil
}

// CleanupExpiredRepoCache cleans up expired cache entries for a specific repo_id and deletes.
func CleanupExpiredRepoCache(db *sql.DB, cacheDir string) error {
	selectSQL := `SELECT repo_id, cid, packfile_name FROM cache WHERE expiry_time < CURRENT_TIMESTAMP;`

	rows, err := db.Query(selectSQL)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var repoID uint64
		var cid, packfileName string
		err = rows.Scan(&repoID, &cid, &packfileName)
		if err != nil {
			continue
		}

		// Delete repository from the file system
		err := os.RemoveAll(path.Join(cacheDir, fmt.Sprintf("%v.git", repoID)))
		if err != nil {
			return err
		}

		// Delete the cache entry
		err = CleanupCacheEntry(db, repoID, cid, packfileName)
		if err != nil {
			return err
		}
	}

	// Check for errors from iterating over rows
	if err = rows.Err(); err != nil {
		return err
	}

	return nil
}
