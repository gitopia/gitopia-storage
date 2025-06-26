# Gitopia Storage Provider

The Gitopia Storage Provider is a self-hosted server that provides storage for Gitopia repositories, LFS objects, and attachments. It integrates with IPFS and IPFS Cluster to ensure data is decentralized, persistent, and content-addressable.

## System Requirements

### Hardware
- **CPU:** 2+ Cores
- **RAM:** 4GB+ (8GB+ recommended for production)
- **Disk:** 4TB

### Storage
The data is stored in three main directories:
- **/var/repos:** Stores the bare Git repositories. This is where all git objects, refs, and hooks reside.
- **/var/lfs-objects:** Stores Git LFS (Large File Storage) objects.
- **/var/attachments:** Stores attachments for releases, issues, and pull requests.

**Planning for Storage:**
- **Mounting External Disks:** For production environments, it is strongly recommended to mount a dedicated, high-performance external disk (e.g., NVMe SSD) to the host machine. You can then point the data directories to this mount point. For Docker, you would map the host mount point to the container volumes. For a manual install, you would set `GIT_REPOS_DIR`, `LFS_OBJECTS_DIR`, and `ATTACHMENT_DIR` in your config file to paths on the mounted disk.

### Software Dependencies
- [git](httpss://git-scm.com/)
- [go](https://golang.org/) 1.23+ (for building from source)
- [IPFS Kubo](https://docs.ipfs.tech/install/command-line/) v0.25+
- [IPFS Cluster](https://ipfscluster.io/documentation/deployment/setup/) v1.0+
- [Docker & Docker Compose](https://www.docker.com/get-started) (for Docker-based installation)

---

## Installation Guide

We offer two methods for installation. The Docker method is recommended for ease of deployment and management.

- **[Installation using Docker (Recommended)](#installation-using-docker-recommended)**
- **[Manual Installation](#manual-installation)**

---

## Installation using Docker (Recommended)

This method uses Docker Compose to set up the entire stack, including the Gitopia Storage Provider, IPFS, and IPFS Cluster.

### 1. Prerequisites
- Docker and Docker Compose are installed on your server.

### 2. Configuration
1.  Clone the repository (if you haven't already).
2.  Create a `.env` file from the example provided:
    ```sh
    cp .env.example .env
    ```
3.  **Edit the `.env` file:**
    - `CLUSTER_SECRET`: **This is critical for security.** You must obtain a secret from the Gitopia team. This secret ensures that only authorized peers can join the cluster and request pinning/unpinning of data.
    - `CLUSTER_PEERNAME`: A unique name for your node in the cluster.
    - `GITOPIA_OPERATOR_MNEMONIC`: The mnemonic for the wallet that will operate the storage provider on the Gitopia chain. **Store this securely.**

### 3. Running the Service
1.  Start all services in the background:
    ```sh
    docker-compose up -d
    ```
2.  Check the logs to ensure everything started correctly:
    ```sh
    docker-compose logs -f gitopia-storage
    docker-compose logs -f cluster
    ```

### 4. One-Time Setup (Registration)
You need to register your provider on the Gitopia blockchain. You only need to do this once.

1.  **Import your key:** The container automatically imports the key from the `GITOPIA_OPERATOR_MNEMONIC` in your `.env` file on startup.

2.  **Register the Provider:**
    Execute the registration command inside the `gitopia-storage` container.
    ```sh
    # Replace <your-public-domain> with the public address of your server
    # Example: http://storage.mydomain.com:5000
    # The amount 1000000000000ulore is an example stake. Adjust as needed.

    docker-compose exec gitopia-storage gitopia-storaged register-provider http://<your-public-domain>:5000 1000000000000ulore --from gitopia-storage --keyring-backend test --fees 200ulore
    ```

Your storage provider is now set up and registered.

### 5. Managing Data and External Disks
The `docker-compose.yml` file maps host directories to container volumes for data persistence.
- `./data/repos` -> `/var/repos` in container
- `./data/attachments` -> `/var/attachments` in container
- `./data/lfs-objects` -> `/var/lfs-objects` in container

To use an external disk, mount it on your host (e.g., at `/mnt/gitopia_data`) and change the mappings in `docker-compose.yml`:
```yaml
volumes:
  - /mnt/gitopia_data/repos:/var/repos
  - /mnt/gitopia_data/attachments:/var/attachments
  - /mnt/gitopia_data/lfs-objects:/var/lfs-objects
```

---

## Manual Installation

This method is for advanced users who want to manage each service directly on the host machine.

### 1. Install Dependencies
Install Go, Git, IPFS Kubo, and IPFS Cluster by following their official documentation.

### 2. IPFS and IPFS Cluster Setup

#### A. IPFS Setup
1.  Initialize IPFS with the server profile:
    ```sh
    ipfs init --profile=server
    ```
2.  We recommend running IPFS as a `systemd` service for production. See the [Production Setup (systemd)](#production-setup-systemd) section below. For now, you can start it manually:
    ```sh
    ipfs daemon &
    ```

#### B. IPFS Cluster Setup
1.  Initialize IPFS Cluster:
    ```sh
    ipfs-cluster-service init
    ```
2.  **Configure IPFS Cluster:** Edit `~/.ipfs-cluster/service.json`. This is a critical step.

    ```json
    {
      "cluster": {
        "peername": "your-unique-peer-name",
        "secret": "your-cluster-secret",
        "trusted_peers": ["/ip4/1.2.3.4/tcp/9096/p2p/Qm..."],
        // ... other settings
      }
    }
    ```
    - **`secret`**: A shared secret required to join the cluster. You must get this from the Gitopia team. It prevents unauthorized peers from connecting.
    - **`trusted_peers`**: An explicit list of peer IDs that are allowed to make changes to the cluster's pinset. This is a crucial security measure. Using `"*"` is **highly discouraged** as it allows *any* peer in the cluster to pin or unpin content. You should get the list of trusted peer multiaddresses from the Gitopia team.

3.  Start the IPFS Cluster service. We recommend using `systemd`. See the [Production Setup (systemd)](#production-setup-systemd) section. For now, you can start it manually with the bootstrap peers provided by the Gitopia team:
    ```sh
    ipfs-cluster-service daemon --bootstrap <peer-multiaddress1,peer-multiaddress2> &
    ```

### 3. Build and Install Gitopia Storage Provider
1.  Clone the repository and build the binaries:
    ```sh
    git clone https://github.com/gitopia/gitopia-storage.git
    cd gitopia-storage
    make build
    ```
2.  Install the binaries to your system's PATH:
    ```sh
    sudo make install
    ```
    This will copy `gitopia-storaged`, `gitopia-pre-receive`, and `gitopia-post-receive` to `/usr/local/bin`.

### 4. Configure Gitopia Storage Provider
1.  Copy the production config file to a system location:
    ```sh
    sudo mkdir -p /etc/gitopia-storage
    sudo cp config_prod.toml /etc/gitopia-storage/config.toml
    ```
2.  Edit `/etc/gitopia-storage/config.toml` and set the correct paths and values, especially for your data directories.

### 5. Run the Service
1.  Create necessary directories:
    ```sh
    sudo mkdir -p /var/repos /var/attachments /var/lfs-objects
    sudo chown -R <your-service-user>:<your-service-user> /var/repos /var/attachments /var/lfs-objects
    ```
2.  Set up your provider key:
    ```sh
    # Run as the user that will run the service
    gitopia-storaged keys add gitopia-storage --recover
    ```
3.  Register the provider (see Docker section for command details).
4.  Start the service. We strongly recommend using the `systemd` service file provided below.

---

## Production Setup (systemd)

For a manual installation in a production environment, you should run `ipfs`, `ipfs-cluster-service`, and `gitopia-storaged` as systemd services.

Create a dedicated user to run the services:
```sh
sudo useradd --system --no-create-home --shell /bin/false gitopia
# Grant ownership of data directories
sudo chown -R gitopia:gitopia /var/repos /var/lfs-objects /var/attachments /etc/gitopia-storage
# Grant ownership of IPFS/IPFS-Cluster config
sudo chown -R gitopia:gitopia /home/gitopia/.ipfs /home/gitopia/.ipfs-cluster
```

#### 1. `ipfs.service`
_File: `/etc/systemd/system/ipfs.service`_
```ini
[Unit]
Description=IPFS Daemon
After=network.target

[Service]
User=gitopia
Group=gitopia
ExecStart=/usr/local/bin/ipfs daemon
Restart=always
LimitNOFILE=10240

[Install]
WantedBy=multi-user.target
```

#### 2. `ipfs-cluster.service`
_File: `/etc/systemd/system/ipfs-cluster.service`_
```ini
[Unit]
Description=IPFS Cluster Daemon
After=ipfs.service
Requires=ipfs.service

[Service]
User=gitopia
Group=gitopia
ExecStart=/usr/local/bin/ipfs-cluster-service daemon
Restart=always

[Install]
WantedBy=multi-user.target
```

#### 3. `gitopia-storaged.service`
_File: `/etc/systemd/system/gitopia-storaged.service`_
```ini
[Unit]
Description=Gitopia Storage Provider
After=network.target ipfs-cluster.service
Requires=ipfs-cluster.service

[Service]
User=gitopia
Group=gitopia
Type=simple
ExecStart=/usr/local/bin/gitopia-storaged start --from gitopia-storage --config /etc/gitopia-storage/config.toml
Restart=on-failure
RestartSec=10
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

#### Enable and Start Services
```sh
sudo systemctl daemon-reload
sudo systemctl enable --now ipfs ipfs-cluster gitopia-storaged
sudo systemctl status ipfs ipfs-cluster gitopia-storaged
```

---

## API and Configuration Details

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

# Cache Configuration
CACHE_REPO_MAX_AGE = "24h"           # Maximum age for repository cache entries
CACHE_REPO_MAX_SIZE = 10737418240    # Maximum size for repository cache (10GB in bytes)
CACHE_ASSET_MAX_AGE = "168h"         # Maximum age for asset cache entries (7 days)
CACHE_ASSET_MAX_SIZE = 5368709120    # Maximum size for asset cache (5GB in bytes)
CACHE_CLEAR_INTERVAL = "1h"          # Interval for cache cleanup
```

### Git Hooks Configuration

The git hooks (`gitopia-pre-receive` and `gitopia-post-receive`) are essential components that integrate with Git's hook system to handle repository operations. These hooks need to be properly configured and available in your system's PATH.

1. Configure the gRPC host for gitopia-pre-receive:
   - The gRPC host is configured in `hooks/gitopia-pre-receive/config/config_prod.go`
   - For production, it's set to `gitopia-grpc.polkachu.com:11390`
   - You can modify this if you're using a different gRPC endpoint

2. Make the hooks available in PATH:
```sh
# After building, copy the hooks to a directory in your PATH
sudo cp build/gitopia-pre-receive /usr/local/bin/
sudo cp build/gitopia-post-receive /usr/local/bin/

# Verify the hooks are available
which gitopia-pre-receive
which gitopia-post-receive
```

Purpose of the hooks:
- `gitopia-pre-receive`: Runs before changes are accepted into the repository. It validates pushes by checking if force pushes are allowed for the target branch. If force pushes are not allowed, it will reject non-fast-forward pushes to protect repository history.
- `gitopia-post-receive`: Runs after changes are accepted. It handles post-push operations by creating dangling references for force pushes and deletions. This ensures that even when history is rewritten or branches are deleted, the previous state is preserved in the repository's reference history.

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
- `POST` /commits
- `GET` /raw/<id>/<repoName>/<branchName>/<filePath> : get raw file

## Contributing

Gitopia is an open source project and contributions from community are always welcome. Discussion and development of Gitopia majorly take place on the Gitopia via issues and proposals -- everyone is welcome to post bugs, feature requests, comments and pull requests to Gitopia. (read [Contribution Guidelines](CONTRIBUTING.md) and [Coding Guidelines](CodingGuidelines.md).
