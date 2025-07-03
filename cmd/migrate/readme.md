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
- git-remote-gitopia

## Configuration

Make changes to config.toml

config.toml
```yaml
GITOPIA_ADDR = "localhost:9100"
GIT_REPOS_DIR = "var/repos"
ATTACHMENT_DIR = "var/attachments"
TM_ADDR = "http://localhost:26667"
WORKING_DIR = "/tmp/test"
GAS_PRICES = "0.001ulore"
CHAIN_ID = "gitopia"
GIT_SERVER_HOST = "http://localhost:5001"

# IPFS Configuration
IPFS_CLUSTER_PEER_HOST = "localhost"
IPFS_CLUSTER_PEER_PORT = "9094"
IPFS_HOST = "localhost"
IPFS_PORT = "5003"
```

## Usage

Setup the key
```bash
migrate keys add gitopia-storage --keyring-backend test --recover
```

Run the migration
```bash
migrate --from gitopia-storage --keyring-backend test
```
