# Gitopia Storage Provider

Storage provider and git apis for [gitopia](https://gitopia.org/)

## Dependencies

- [git](https://git-scm.com/)
- [go](https://golang.org/) 1.23 or later

### Build

#### Local Build

To build the services locally:

```sh
# Build all binaries
make build

# Install binaries to $GOPATH/bin
make install
```

The build process will create the following binaries in the `build/` directory:
- `git-server`: Main server binary
- `gitopia-pre-receive`: Git pre-receive hook
- `gitopia-post-receive`: Git post-receive hook
- `migrate`: Storage migration tool

#### Docker Build

By default, git-server is built with local configurations. If you want to build with `PRODUCTION` or `DEVELOPMENT` configurations, pass a build arg `ENV` with respective value.

```sh
make docker-build-debug
```

### Configuration

The server can be configured using a TOML configuration file. Here's the complete configuration structure:

```toml
# Server Configuration
WEB_SERVER_PORT = 5000
GIT_DIR = "/var/repos"
LFS_OBJECTS_DIR = "/var/lfs-objects"
ATTACHMENT_DIR = "/var/attachments"
WORKING_DIR = "/home/ubuntu/git-server/"

# Gitopia Network Configuration
GITOPIA_ADDR = "gitopia-grpc.polkachu.com:11390"
TM_ADDR = "https://gitopia-rpc.polkachu.com:443"
GIT_SERVER_EXTERNAL_ADDR = "https://server.gitopia.com"
CHAIN_ID = "gitopia"
GAS_PRICES = "0.001ulore"

# IPFS Configuration
IPFS_CLUSTER_PEER_HOST = "your-ipfs-host"
IPFS_CLUSTER_PEER_PORT = "your-ipfs-port"
IPFS_HOST = "your-ipfs-host"
IPFS_PORT = "your-ipfs-port"
ENABLE_EXTERNAL_PINNING = false
PINATA_JWT = "your-pinata-jwt"  # Required if ENABLE_EXTERNAL_PINNING is true
```

### Usage

#### Local Usage

1. Create necessary directories and set permissions:
```sh
mkdir -p /var/repos /var/attachments /var/lfs-objects
chmod 755 /var/repos /var/attachments /var/lfs-objects
```

2. Set up key and register as provider:
```sh
# Generate or recover key based on server ID
git-server keys add git-server --keyring-backend test --recover

# Register as provider (adjust amount as needed)
git-server register-provider http://localhost:5000 1000000000000ulore --from git-server --keyring-backend test --fees 200ulore
```

3. Start the server:
```sh
git-server start --from git-server --keyring-backend test
```

#### Docker Usage

1. Create necessary directories and set permissions:
```sh
mkdir -p /var/repos /var/attachments /var/lfs-objects
chmod 755 /var/repos /var/attachments /var/lfs-objects
```

2. Start the container:
```sh
docker run -it \
  --name git-server \
  --mount type=bind,source=/var/attachments,target=/var/attachments \
  --mount type=bind,source=/var/repos,target=/var/repos \
  -p 5000:5000 \
  gitopia/git-server
```

> **Important**  
> Make sure that `source`, `target` in the docker run command and the `GIT_DIR` in the configuration file have the same path. This is required because forked repositories link to parent repositories via the git alternates mechanism wherein the absolute path of the parent repository is stored in the forked repository's alternates file.

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
- `GET` /raw/<id>/<repoName>/<branchName>/<filePath> : get raw file

## Contributing

Gitopia is an open source project and contributions from community are always welcome. Discussion and development of Gitopia majorly take place on the Gitopia via issues and proposals -- everyone is welcome to post bugs, feature requests, comments and pull requests to Gitopia. (read [Contribution Guidelines](CONTRIBUTING.md) and [Coding Guidelines](CodingGuidelines.md).
