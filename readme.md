# Gitopia Storage Provider

Storage provider and git apis for [gitopia](https://gitopia.org/)

## Dependencies

- [git](https://git-scm.com/)
- [go](https://golang.org/) 1.23 or later
- [IPFS Kubo](https://docs.ipfs.tech/install/command-line/) - IPFS daemon
- [IPFS Cluster](https://ipfscluster.io/documentation/deployment/setup/) - IPFS Cluster daemon

### IPFS and IPFS Cluster Setup

#### 1. IPFS Setup

1. Install IPFS Kubo following the [official installation guide](https://docs.ipfs.tech/install/command-line/) or use the following commands for Linux:

```sh
# Download and install IPFS Kubo v0.35.0
wget https://dist.ipfs.tech/kubo/v0.35.0/kubo_v0.35.0_linux-amd64.tar.gz
tar -xvzf kubo_v0.35.0_linux-amd64.tar.gz
cd kubo
sudo bash install.sh

# Verify installation
ipfs --version
# Should output: ipfs version 0.35.0
```

2. Initialize IPFS with server profile for production use:
```sh
ipfs init --profile=server
```

3. Start IPFS daemon:
```sh
ipfs daemon
```

#### 2. IPFS Cluster Setup

1. Install IPFS Cluster following the [official installation guide](https://ipfscluster.io/documentation/deployment/setup/) or use the following commands for Linux:

```sh
# Download and install IPFS Cluster Service v1.1.4
wget https://dist.ipfs.tech/ipfs-cluster-service/v1.1.4/ipfs-cluster-service_v1.1.4_linux-amd64.tar.gz
tar xvzf ipfs-cluster-service_v1.1.4_linux-amd64.tar.gz

# Download and install IPFS Cluster Control v1.1.4
wget https://dist.ipfs.tech/ipfs-cluster-ctl/v1.1.4/ipfs-cluster-ctl_v1.1.4_linux-amd64.tar.gz
tar xvzf ipfs-cluster-ctl_v1.1.4_linux-amd64.tar.gz
```

2. Initialize IPFS Cluster:
```sh
ipfs-cluster-service init
# This will create configuration files in ~/.ipfs-cluster/
```

3. Configure IPFS Cluster to join the Gitopia storage network:
```sh
# Edit the service.json configuration file
# Location: ~/.ipfs-cluster/service.json

{
  "cluster": {
    "peername": "your-peer-name",
    "secret": "your-cluster-secret", # Get this from Gitopia team
    "trusted_peers": ["peer-multiaddress1","peer-multiaddress2"], # Trust peers in the cluster
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
ipfs-cluster-service daemon --bootstrap <peer-multiaddress1,peer-multiaddress2>
```

5. Verify cluster connection and status:
```sh
# Check cluster peers
ipfs-cluster-ctl peers ls

# Verify the cluster is ready by checking logs
# You should see: "INFO    cluster: ** IPFS Cluster is READY **"
```

### Build

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
