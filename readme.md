# Gitopia Storage Provider

Storage provider and git apis for [gitopia](https://gitopia.org/)

## Dependencies

- [git](https://git-scm.com/)
- [go](https://golang.org/) 1.23 or later
- [IPFS Kubo](https://docs.ipfs.tech/install/command-line/) - IPFS daemon
- [IPFS Cluster](https://ipfscluster.io/documentation/deployment/setup/) - IPFS Cluster daemon

### IPFS and IPFS Cluster Setup

#### 1. IPFS Setup

1. Install IPFS Kubo following the [official installation guide](https://docs.ipfs.tech/install/command-line/)

2. Initialize IPFS with server profile for production use:
```sh
ipfs init --profile=server
```

3. Configure IPFS for production use:
```sh
# Increase connection manager limits
ipfs config Swarm.ConnMgr.HighWater 10000
ipfs config Swarm.ConnMgr.LowWater 2500
ipfs config Swarm.ConnMgr.GracePeriod 20s

# Enable experimental DHT providing
ipfs config --bool Experimental.AcceleratedDHTClient true

# Set file descriptor limit
export IPFS_FD_MAX=8192

# Configure datastore for better performance
ipfs config Datastore.BloomFilterSize 1048576
ipfs config Datastore.StorageMax "100GB" # Adjust based on your disk size
```

4. Start IPFS daemon:
```sh
ipfs daemon
```

#### 2. IPFS Cluster Setup

1. Install IPFS Cluster following the [official installation guide](https://ipfscluster.io/documentation/deployment/setup/)

2. Initialize IPFS Cluster:
```sh
ipfs-cluster-service init
```

3. Configure IPFS Cluster to join the Gitopia storage network:
```sh
# Edit the service.json configuration file
# Location: ~/.ipfs-cluster/service.json

{
  "cluster": {
    "peername": "your-peer-name",
    "secret": "your-cluster-secret", # Get this from Gitopia team
    "trusted_peers": ["*"], # Trust all peers in the cluster
    "ipfs_connector": {
      "ipfshttp": {
        "node_multiaddress": "/ip4/127.0.0.1/tcp/5001"
      }
    },
    "restapi": {
      "http_listen_multiaddress": "/ip4/0.0.0.0/tcp/9094"
    },
    "monitor": {
      "ping_interval": "2s"
    }
  }
}
```

4. Start IPFS Cluster service:
```sh
ipfs-cluster-service daemon
```

5. Verify cluster connection:
```sh
ipfs-cluster-ctl peers ls
```

#### 3. Docker Setup

##### Single Provider Setup

1. Create a `.env` file with required environment variables:
```sh
CLUSTER_SECRET=your-cluster-secret  # Required - Get from Gitopia team
CLUSTER_PEERNAME=your-peer-name     # Optional - Defaults to 'storage-provider'
ENABLE_EXTERNAL_PINNING=false       # Optional - Enable Pinata pinning
PINATA_API_KEY=your-pinata-key      # Optional - Required if ENABLE_EXTERNAL_PINNING is true
PINATA_SECRET_KEY=your-pinata-secret # Optional - Required if ENABLE_EXTERNAL_PINNING is true
```

2. Create necessary directories:
```sh
mkdir -p data/{ipfs,cluster,repos,attachments,lfs-objects}
```

3. Start the services:
```sh
docker-compose up -d
```

##### Test Environment Setup (Multiple Providers)

For local testing and development, you can set up multiple storage providers using the test environment configuration:

1. Create the test network:
```sh
docker network create storage-provider-test
```

2. Create a `.env` file with required environment variables:
```sh
CLUSTER_SECRET=your-test-secret  # Required - Use any secret for local testing
ENABLE_EXTERNAL_PINNING=false    # Optional - Enable Pinata pinning
PINATA_API_KEY=your-pinata-key   # Optional - Required if ENABLE_EXTERNAL_PINNING is true
PINATA_SECRET_KEY=your-pinata-secret # Optional - Required if ENABLE_EXTERNAL_PINNING is true
```

3. Create necessary directories:
```sh
mkdir -p compose/{ipfs0,ipfs1,ipfs2,cluster0,cluster1,cluster2,gitopia-storage0,gitopia-storage1,gitopia-storage2}
```

4. Start the test environment:
```sh
docker-compose -f docker-compose.test.yml up -d
```

This will start 3 storage providers with their respective IPFS and Cluster peers, all connected in a test network.

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
- `gitopia-storaged`: Storage provider binary
- `gitopia-pre-receive`: Git pre-receive hook
- `gitopia-post-receive`: Git post-receive hook
- `migrate`: Storage migration tool

#### Docker Build

By default, gitopia-storaged is built with production configurations. If you want to build with `local` or `dev` configurations, set GITOPIA_ENV to respective value.

```sh
make docker-build-gitopia-storage
```

### Configuration

The storage provider can be configured using a TOML configuration file. Here's the complete configuration structure:

```toml
# Server Configuration
WEB_SERVER_PORT = 5000
GIT_REPOS_DIR = "/var/repos"
LFS_OBJECTS_DIR = "/var/lfs-objects"
ATTACHMENT_DIR = "/var/attachments"
WORKING_DIR = "/home/ubuntu/gitopia-storage/"

# Gitopia Network Configuration
GITOPIA_ADDR = "gitopia-grpc.polkachu.com:11390"
TM_ADDR = "https://gitopia-rpc.polkachu.com:443"
GIT_SERVER_EXTERNAL_ADDR = "public-address"
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
gitopia-storaged keys add gitopia-storage --keyring-backend test --recover

# Register as provider (adjust amount as needed)
gitopia-storaged register-provider http://localhost:5000 1000000000000ulore --from gitopia-storage --keyring-backend test --fees 200ulore
```

3. Start the server:
```sh
gitopia-storaged start --from gitopia-storage --keyring-backend test
```

#### Docker Usage

1. Create necessary directories and set permissions:
```sh
mkdir -p /var/repos /var/attachments /var/lfs-objects
```

2. Start the container:
```sh
docker run -it \
  --name gitopia-storage \
  --mount type=bind,source=/var/attachments,target=/var/attachments \
  --mount type=bind,source=/var/repos,target=/var/repos \
  -p 5000:5000 \
  gitopia/gitopia-storage
```

> **Important**  
> Make sure that `source`, `target` in the docker run command and the `GIT_REPOS_DIR` in the configuration file have the same path. This is required because forked repositories link to parent repositories via the git alternates mechanism wherein the absolute path of the parent repository is stored in the forked repository's alternates file.

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
