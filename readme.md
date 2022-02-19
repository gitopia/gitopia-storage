# gitopia services

gitopia services for [gitopia](https://gitopia.org/)

## Build

```
docker build . --build-arg ACCESS_TOKEN=<USERNAME>:<PERSONAL_ACCESS_TOKEN> \
  --build-arg ENV=<ENV> \
  -t git-server
```

## Usage

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

## Available APIs

- `GET` /objects/<repository_id>/<object_hash> : get loose git object
- `POST` /save : save newly pushed objects to Arweave
- `POST` /upload : upload release/issue/pull_request/comment attachments
- `GET` /releases/<address>/<repositoryName>/<tagName>/<fileName> : get attachment
- `GET` /info/refs
- `POST` /git-upload-pack
- `POST` /git-receive-pack
- `POST` /fork
- `POST` /pull/diff
- `POST` /pull/commits
- `POST` /pull/check
- `POST` /pull/merge
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
