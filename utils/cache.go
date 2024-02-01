package utils

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gitopia/gitopia-go"
	gitopiatypes "github.com/gitopia/gitopia/v4/x/gitopia/types"
	_ "github.com/mattn/go-sqlite3"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
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
		parent_repo_id INTEGER NOT NULL UNIQUE,
        cid TEXT NOT NULL UNIQUE,
		packfile_name TEXT NOT NULL UNIQUE,
		parent_id INTEGER,
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

func IsCached(db *sql.DB, cid, packfileName string) bool {
	return false

	query := `SELECT COUNT(*) FROM cache WHERE packfile_name = ?`
	stmt, err := db.Prepare(query)
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()

	var count int
	err = stmt.QueryRow(packfileName).Scan(&count)
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

func DownloadRepo(db *sql.DB, id uint64, cacheDir string) error {
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
		err := DownloadRepo(db, res.Repository.Parent, cacheDir)
		if err != nil {
			return errors.Wrap(err, "error downloading parent repo")
		}
	}

	var backup *gitopiatypes.RepositoryBackup
	for i := range res.Repository.Backups {
		if res.Repository.Backups[i].Store == gitopiatypes.RepositoryBackup_IPFS {
			backup = res.Repository.Backups[i]
			break
		}
	}

	if backup == nil {
		return errors.Wrap(err, "backup ref not found")
	}

	// make sure dependent parent repos are there

	if err := DownloadPackfile(backup.Refs[1], backup.Refs[2], cacheDir); err != nil {
		return errors.Wrap(err, "error downloading packfile")
	}

	InsertCacheData(db, id, res.Repository.Parent, backup.Refs[1], backup.Refs[2], time.Now(), time.Now(), time.Now().Add(time.Hour*24))

	return nil
}

func DownloadPackfile(cid string, packfileName string, cacheDir string) error {
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

	return nil
}

// UpdateCache updates the cache duration based on file access
func UpdateCache(db *sql.DB, cid string, packfileName string, additionalDuration time.Duration) {
	// if item, exists := CacheMap[fileName]; exists {
	// 	item.LastAccessed = time.Now()
	// 	item.Duration += additionalDuration
	// 	CacheMap[fileName] = item
	// }
}

// CleanUpCache removes expired files from the cache
func CleanUpCache(cacheDir string) {
	// for fileName, item := range CacheMap {
	// 	if time.Since(item.LastAccessed) > item.Duration {
	// 		os.Remove(fileName)
	// 		delete(CacheMap, fileName)
	// 	}
	// }
}

// // main function to test the cache system
// func main() {
// 	cacheDir := "./cache"                // Define your cache directory here
// 	url := "http://example.com/file.txt" // URL of the file to download

// 	// Ensure cache directory exists
// 	os.MkdirAll(cacheDir, os.ModePerm)

// 	// Download and cache the file
// 	fileName, err := DownloadFile(url, cacheDir)
// 	if err != nil {
// 		panic(err)
// 	}

// 	// Update cache on file access
// 	UpdateCache(fileName, 30*time.Minute) // Increase cache duration by 30 minutes

// 	// Periodically clean up the cache
// 	ticker := time.NewTicker(1 * time.Hour)
// 	go func() {
// 		for range ticker.C {
// 			CleanUpCache(cacheDir)
// 		}
// 	}()
// }
