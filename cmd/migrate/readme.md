# Git server Migration Tool

A command-line tool for migrating Gitopia repositories and releases to IPFS storage.

## Overview

This tool helps migrate existing Gitopia repositories and their releases to IPFS storage by:
- Processing git repositories and their packfiles
- Migrating release attachments
- Computing merkle roots for verification
- Updating repository and release information on the Gitopia blockchain

## Prerequisites

- IPFS node
- IPFS Cluster
- Gitopia blockchain node access

## Configuration

The tool requires several configuration parameters:

```bash
# Git directories
--git-dir              # Directory containing git repositories
--attachment-dir       # Directory containing release attachments

# IPFS Configuration
--ipfs-cluster-peer-host  # IPFS cluster peer host
--ipfs-cluster-peer-port  # IPFS cluster peer port
--ipfs-host            # IPFS host
--ipfs-port            # IPFS port

# Gitopia Configuration
--chain-id            # Chain ID
--gas-prices          # Gas prices
--gitopia-addr        # Gitopia address
--tm-addr             # Tendermint address
--working-dir         # Working directory
```

## Usage

```bash
migrate --git-dir /path/to/repos \
        --attachment-dir /path/to/attachments \
        --ipfs-cluster-peer-host localhost \
        --ipfs-cluster-peer-port 9094 \
        --ipfs-host localhost \
        --ipfs-port 5001 \
        --chain-id your-chain-id \
        --gas-prices 0.1ulore \
        --gitopia-addr http://localhost:1317 \
        --tm-addr http://localhost:26657 \
        --working-dir /path/to/working/dir
```
