# Gitopia Storage Provider

The Gitopia Storage Provider is a self-hosted server that provides storage for Gitopia repositories, LFS objects, and attachments. It integrates with IPFS and IPFS Cluster to ensure data is decentralized, persistent, and content-addressable.

## System Requirements

### Hardware
- **CPU:** 2+ Cores
- **RAM:** 8GB+
- **Disk:** 1TB

### Storage

The Gitopia Storage Provider leverages a layered storage architecture, with the primary and most significant storage requirement being the IPFS data directory. This ensures all repository content is decentralized, persistent, and content-addressable, while local directories act as caches to optimize performance.

- **IPFS Data Directory:**
  - The main storage location for all repository data, LFS objects, and attachments is the IPFS data directory (typically `~/.ipfs` inside the container or mapped to a host directory). This directory should be allocated the majority of your disk space, as it is responsible for persisting and pinning all content.
  - For production, it is strongly recommended to map the IPFS data directory to a dedicated, high-performance external disk (such as an NVMe SSD) for optimal reliability and speed.

- **Cache Directories:**
  - **/var/repos:** Acts as a cache for bare Git repositories (git objects, refs, and hooks). Storage usage depends on your cache configuration and retention policy.
  - **/var/lfs-objects:** Stores Git LFS (Large File Storage) objects. This can be configured to point to a preferred directory.
  - **/var/attachments:** Caches attachments (releases, issues, pull requests). Storage usage is also determined by your cache settings.
  - These directories are configurable and serve to speed up access and operations. Their disk usage can be tuned by adjusting cache size and retention policies. They do not serve as the main persistent storage.

> **Note:** Only the IPFS data directory is responsible for long-term, reliable storage. The cache directories are secondary and can be sized according to your operational needs.

**Planning for Storage:**
- Allocate most disk space to the IPFS data directory.
- Adjust cache directory sizes and policies as appropriate for your environment.
- For Docker, map the relevant host directories to the container volumes. For manual installs, set `GIT_REPOS_DIR`, `LFS_OBJECTS_DIR`, and `ATTACHMENT_DIR` in your config file to desired paths.

### Software Dependencies
- [git](httpss://git-scm.com/)
- [go](https://golang.org/) 1.23+ (for building from source)
- [IPFS Kubo](https://docs.ipfs.tech/install/command-line/) v0.25+
- [IPFS Cluster](https://ipfscluster.io/documentation/deployment/setup/) v1.0+
- [Docker & Docker Compose](https://www.docker.com/get-started) (for Docker-based installation)

---

## Security Considerations

When deploying the Gitopia Storage Provider in a production environment, it's crucial to follow security best practices to protect your data and infrastructure. This section outlines key security considerations based on IPFS Cluster's guidelines.

### Cluster Secret

The `CLUSTER_SECRET` is a 32-byte hex-encoded pre-shared key that acts as a network protector for libp2p communications between cluster peers. It provides additional encryption and is essential for network isolation.

- **Always set a strong, unique secret:** Never run with an empty secret. It prevents your cluster peers from accidentally discovering and connecting to the public IPFS network.
- **Keep it private:** This secret should be treated like a password. Share it only with trusted cluster peers.

### Trusted Peers (CRDT Mode)

In CRDT mode, the `CLUSTER_TRUSTED_PEERS` configuration controls which peers are authorized to modify the cluster's pinset and perform administrative actions. This is a critical security measure.

- **Never use `*` in production:** The default Docker Compose configuration uses `CLUSTER_CRDT_TRUSTEDPEERS=*`, which trusts any peer. This is highly insecure for production deployments.
- **Explicitly list trusted peers:** Configure `CLUSTER_TRUSTED_PEERS` with the specific multiaddresses of trusted nodes. You can retrieve these addresses using:
  ```sh
  ./gitopia-storaged get-ipfs-cluster-peer-addresses
  ```
  Then update your `.env` file or `service.json` accordingly.

### Port Security

IPFS Cluster uses several ports for different purposes. Properly securing these ports is vital to prevent unauthorized access.

- **Cluster Swarm (`tcp:9096`):**
  - Controlled by `cluster.listen_multiaddress` (defaults to `/ip4/0.0.0.0/tcp/9096`).
  - Protected by the shared `CLUSTER_SECRET`.
  - **It is generally safe to expose this port**, but ensure your cluster secret is strong.

- **HTTP API (`tcp:9094`) and IPFS Pinning Service API (`tcp:9097`):**
  - These endpoints provide full administrative control over the cluster peer.
  - By default, they listen on `127.0.0.1` for security.
  - **Only expose these ports if absolutely necessary**, and always configure SSL and Basic Authentication.

- **IPFS API (`tcp:5001`) and IPFS Gateway (`tcp:8080`):**
  - These are the standard IPFS Kubo ports.
  - The API should **never** be exposed to the public internet. It should only listen on `127.0.0.1`.
  - The Gateway (`8080`) can be exposed if you intend to serve content publicly, but consider using a reverse proxy with rate limiting and security headers.

- **IPFS Proxy (`tcp:9095`):**
  - Controlled by `ipfshttp.proxy_listen_multiaddress` (defaults to `/ip4/127.0.0.1/tcp/9095`).
  - This endpoint mimics the IPFS API and should **never** be exposed without authentication.
  - It runs on localhost by default, which is secure.

- **Gitopia Storage Provider (`tcp:5000`):**
  - This is the main application port.

For Docker deployments, review the port mappings in `docker-compose.yml` and ensure only necessary ports are exposed to the host.

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

> [!WARNING] 
> Make sure to sync your ipfs cluster with other active providers before registering your provider. You'll start receiving challenges after registration.

> [!IMPORTANT]
> In addition to the stake requirement, your Gitopia account must maintain a minimum balance of **100 lore** to perform storage operations. The storage provider will check this balance on startup and reject operations if insufficient funds are available.

1.  **Import your key:** The container automatically imports the key from the `GITOPIA_OPERATOR_MNEMONIC` in your `.env` file on startup.

2.  **Register the Provider:**
    Execute the registration command inside the `gitopia-storage` container.
    ```sh
    # Replace <your-public-domain> with the public address of your server
    # Example: http://storage.mydomain.com:5000
    # Replace <your-cluster-peer-multiaddress> with the multiaddress of your cluster peer
    # You can get the multiaddress of your cluster peer by running the following command
    # ipfs-cluster-ctl id
    # Example: /ip4/104.244.178.22/tcp/9096/p2p/12D3KooWHWG333xgo3QnZTSP9trGmGBvs37JsqwiUz5XYHjMpgfA
    # The amount 1000000000000ulore is an example stake. Adjust as needed.

    docker-compose exec gitopia-storage gitopia-storaged register-provider http://<your-public-domain>:5000 "My Provider Description" 1000000000000ulore <your-cluster-peer-multiaddress> --from gitopia-storage --keyring-backend test --fees 200ulore
    ```

    You can update the provider api url, description and multiaddress by running the following command:
    ```sh
    docker-compose exec gitopia-storage gitopia-storaged update-provider http://<your-public-domain>:5000 "New Provider Description" <your-cluster-peer-multiaddress> --from gitopia-storage --keyring-backend test --fees 200ulore
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

#### Install Go and Git
Install Go (1.23+) and Git by following their official documentation:
- [Go Installation Guide](https://golang.org/doc/install)
- [Git Installation Guide](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)

#### Install IPFS Kubo Binary
Download and install IPFS Kubo v0.37.0:

```bash
# Download IPFS Kubo
wget https://dist.ipfs.tech/kubo/v0.37.0/kubo_v0.37.0_linux-amd64.tar.gz

# Extract the archive
tar -xvzf kubo_v0.37.0_linux-amd64.tar.gz

# Move to system PATH
cd kubo
sudo mv ipfs /usr/local/bin/

# Verify installation
ipfs version
```

#### Install IPFS Cluster Service
Download and install IPFS Cluster Service v1.1.4:

```bash
# Download IPFS Cluster Service
curl -O https://dist.ipfs.tech/ipfs-cluster-service/v1.1.4/ipfs-cluster-service_v1.1.4_linux-amd64.tar.gz

# Extract the archive
tar -xvzf ipfs-cluster-service_v1.1.4_linux-amd64.tar.gz

# Move to system PATH
cd ipfs-cluster-service/
sudo mv ipfs-cluster-service /usr/local/bin/

# Verify installation
ipfs-cluster-service --version
```

#### Install IPFS Cluster Control Tool
Download and install IPFS Cluster Control Tool v1.1.4:

```bash
# Download IPFS Cluster Control Tool
curl -O https://dist.ipfs.tech/ipfs-cluster-ctl/v1.1.4/ipfs-cluster-ctl_v1.1.4_linux-amd64.tar.gz

# Extract the archive
tar -xvzf ipfs-cluster-ctl_v1.1.4_linux-amd64.tar.gz

# Move to system PATH
cd ipfs-cluster-ctl/
sudo mv ipfs-cluster-ctl /usr/local/bin/

# Verify installation
ipfs-cluster-ctl --version
```

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
    - **`trusted_peers`**: An explicit list of peer IDs that are allowed to make changes to the cluster's pinset. This is a crucial security measure. Using `"*"` is **highly discouraged** as it allows *any* peer in the cluster to pin or unpin content.

You can retrieve the multiaddresses of all active storage providers (to use as trusted peers) by running the following command:

```sh
./gitopia-storaged get-ipfs-cluster-peer-addresses
```

This command will output a comma-separated list of peer multiaddresses. Copy these addresses and add them to the `trusted_peers` field in your IPFS Cluster configuration file (usually `service.json`).

**Example:**

```
"trusted_peers": [
  "/ip4/1.2.3.4/tcp/9096/p2p/12D3KooW...",
  "/ip4/5.6.7.8/tcp/9096/p2p/12D3KooX..."
]
```

Be sure to update this list whenever the set of active storage providers changes.

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
    sudo cp config_prod.toml /etc/gitopia-storage/config_prod.toml
    ```
2.  Edit `/etc/gitopia-storage/config_prod.toml` and set the correct paths and values, especially for your data directories.

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

### Initial Setup

Create a dedicated user to run the services:
```sh
# Create system user for gitopia services
sudo useradd --system --create-home --shell /bin/false gitopia

# Create necessary directories
sudo mkdir -p /var/repos /var/lfs-objects /var/attachments /etc/gitopia-storage
sudo mkdir -p /home/gitopia/.ipfs /home/gitopia/.ipfs-cluster

# Grant ownership of data directories
sudo chown -R gitopia:gitopia /var/repos /var/lfs-objects /var/attachments /etc/gitopia-storage
sudo chown -R gitopia:gitopia /home/gitopia/.ipfs /home/gitopia/.ipfs-cluster

# Initialize IPFS and IPFS Cluster as the gitopia user
sudo -u gitopia ipfs init --profile=server
sudo -u gitopia ipfs-cluster-service init
```

### Configure IPFS and IPFS Cluster

Before creating the systemd services, ensure IPFS and IPFS Cluster are properly configured:

```sh
# Configure IPFS to listen on all interfaces (optional, for cluster communication)
sudo -u gitopia ipfs config Addresses.API /ip4/127.0.0.1/tcp/5001
sudo -u gitopia ipfs config Addresses.Gateway /ip4/127.0.0.1/tcp/8080

# Configure IPFS StorageMax (adjust based on your disk space)
sudo -u gitopia ipfs config Datastore.StorageMax 500GB

# Edit IPFS Cluster configuration
sudo -u gitopia nano /home/gitopia/.ipfs-cluster/service.json
```

Make sure to configure the `service.json` file with your cluster secret and trusted peers as described in the manual installation section.

### Systemd Service Files

#### 1. `ipfs.service`
_File: `/etc/systemd/system/ipfs.service`_
```ini
[Unit]
Description=IPFS Daemon
After=network.target
Documentation=https://docs.ipfs.tech/

[Service]
Type=notify
User=gitopia
Group=gitopia
Environment=IPFS_PATH="/home/gitopia/.ipfs"
ExecStart=/usr/local/bin/ipfs daemon --enable-gc
ExecStop=/usr/local/bin/ipfs shutdown
Restart=always
RestartSec=10
KillMode=mixed
KillSignal=SIGINT
TimeoutStopSec=30
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
Documentation=https://ipfscluster.io/

[Service]
Type=simple
User=gitopia
Group=gitopia
Environment=IPFS_CLUSTER_PATH=/home/gitopia/.ipfs-cluster
ExecStart=/usr/local/bin/ipfs-cluster-service daemon
ExecStop=/usr/local/bin/ipfs-cluster-ctl shutdown
Restart=always
RestartSec=10
KillMode=mixed
TimeoutStopSec=30

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
Documentation=https://github.com/gitopia/gitopia-storage

[Service]
Type=simple
User=gitopia
Group=gitopia
Environment=ENV=PRODUCTION
Environment=HOME=/home/gitopia
WorkingDirectory=/home/gitopia
ExecStart=/usr/local/bin/gitopia-storaged start --from gitopia-storage --keyring-backend test
Restart=on-failure
RestartSec=10
KillMode=mixed
TimeoutStopSec=30
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
```

#### Enable and Start Services

After creating all service files, enable and start the services:

```sh
# Reload systemd configuration
sudo systemctl daemon-reload

# Enable services to start on boot
sudo systemctl enable ipfs ipfs-cluster gitopia-storaged

# Start services in order
sudo systemctl start ipfs
sudo systemctl start ipfs-cluster
sudo systemctl start gitopia-storaged

# Check service status
sudo systemctl status ipfs ipfs-cluster gitopia-storaged

# View logs if needed
sudo journalctl -u ipfs -f
sudo journalctl -u ipfs-cluster -f
sudo journalctl -u gitopia-storaged -f
```

#### Service Management Commands

Useful commands for managing the services:

```sh
# Stop services
sudo systemctl stop gitopia-storaged ipfs-cluster ipfs

# Restart services
sudo systemctl restart ipfs ipfs-cluster gitopia-storaged

# Check if services are running
sudo systemctl is-active ipfs ipfs-cluster gitopia-storaged

# View detailed service information
sudo systemctl show ipfs
sudo systemctl show ipfs-cluster
sudo systemctl show gitopia-storaged
```

---

## API and Configuration Details

### IPFS Storage Configuration

#### Increasing IPFS StorageMax

The IPFS `StorageMax` setting controls the maximum amount of data that IPFS will store locally. For gitopia-storage deployments, you may need to increase this limit based on your storage requirements.

**For Docker Setup:**
```bash
# Access the IPFS container
docker exec -it ipfs sh

# Check current StorageMax setting
ipfs config Datastore.StorageMax

# Set new StorageMax (example: 500GB)
ipfs config Datastore.StorageMax 500GB

# Restart the IPFS container to apply changes
docker restart ipfs
```

**For Manual Installation:**
```bash
# Check current StorageMax setting
ipfs config Datastore.StorageMax

# Set new StorageMax (example: 500GB)
ipfs config Datastore.StorageMax 500GB

# Restart IPFS daemon
sudo systemctl restart ipfs
```

**Important Notes:**
- Ensure sufficient disk space: The StorageMax should not exceed your available disk space
- For Docker, this affects the `ipfs_data` volume
- Monitor usage with `ipfs repo stat`
- IPFS will automatically run garbage collection when approaching the limit

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
CACHE_LFS_MAX_AGE = "720h"           # Maximum age for LFS object cache entries (30 days)
CACHE_LFS_MAX_SIZE = 53687091200     # Maximum size for LFS object cache (50GB in bytes)
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
