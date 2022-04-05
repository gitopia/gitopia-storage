# gitopia services

gitopia services for [gitopia](https://gitopia.org/)

## Web server

### Build

```
docker build . --build-arg USER=<USER> \
  --build-arg PERSONAL_ACCESS_TOKEN=<PERSONAL_ACCESS_TOKEN> \
  --build-arg ENV=<ENV> \
  -t git-server
```

### Usage

Make necessary changes in `config.toml` for production and also set the following environment variable. Create `git_dir` and `attachments_dir` and verify the permissions.

To start the server, execute the following command

```sh
docker run -it \
  --name git-server \
  --mount type=bind,source="$(pwd)/../tmp",target=/var/attachments \
  --mount type=bind,source="$(pwd)/../tmp",target=/var/repos -p 5000:5000 \
  git-server
```

The server will be listening at port `5000`

### Available APIs

- `GET` /objects/<repository_id>/<object_hash> : get loose git object
- `POST` /upload : upload release/issue/pull_request/comment attachments
- `GET` /releases/<address>/<repositoryName>/<tagName>/<fileName> : get attachment
- `GET` /info/refs
- `POST` /git-upload-pack
- `POST` /git-receive-pack
- `POST` /pull/diff
- `POST` /pull/commits
- `POST` /pull/check
- `POST` /content
  ```go
  type ContentRequestBody struct {
    RepositoryID uint64       `json:"repository_id"`
    RefId        string       `json:"ref_id"`
    Path         string       `json:"path"`
    Pagination   *PageRequest `json:"pagination"`
  }
  ```
- `POST` /commits
  ```go
  type CommitsRequestBody struct {
    RepositoryID uint64       `json:"repository_id"`
    InitCommitId string       `json:"init_commit_id"`
    Path         string       `json:"path"`
    Pagination   *PageRequest `json:"pagination"`
  }
  ```

## Event processing service

### Build

```
make build
```

### Usage

Make necessary changes in `config.toml` for production and also set the following environment variable. Create `git_dir` and verify the permissions.

Create a key for git-server-events and send some tokens to it's address

```sh
./build/git-server-events keys add git-server --keyring-backend test
```

Get address

```sh
./build/git-server-events keys show git-server -a --keyring-backend test
```

To start the service, execute the following command

```sh
./build/git-server-events run
```

Logs will be available at `gitopia-git-server-events.log`
