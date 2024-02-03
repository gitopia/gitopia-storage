package db

import (
	"database/sql"

	"github.com/gitopia/git-server/utils"
)

var CacheDb *sql.DB

func OpenDb(path string) error {
	if !utils.DbExists(path) {
		CacheDb = utils.InitializeDB(path)
	} else {
		var err error
		CacheDb, err = sql.Open("sqlite3", path)
		if err != nil {
			return err
		}
	}
	return nil
}
