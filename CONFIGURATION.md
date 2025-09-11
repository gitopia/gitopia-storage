# Configuration Guide

This document explains the simplified configuration system for Gitopia Storage Provider.

## Overview

The new configuration system uses a **hierarchy approach** with production defaults and easy overrides:

1. **`config.toml`** - Base configuration with production defaults
2. **`config.local.toml`** - Local development overrides (gitignored)
3. **Environment variables** - Runtime overrides (highest priority)

## Configuration Priority (highest to lowest)

1. **Environment Variables** - Override any setting at runtime
2. **config.local.toml** - Local development overrides
3. **config.toml** - Base production configuration

## Usage Scenarios

### Production Deployment

**Default behavior**: Uses `config.toml` with production settings.

```bash
# Production uses config.toml by default
./gitopia-storaged start

# Override specific settings with environment variables
GITOPIA_ADDR="custom-grpc.example.com:9090" ./gitopia-storaged start
```

### Local Development

**Step 1**: Copy the example file
```bash
cp config.local.toml.example config.local.toml
```

**Step 2**: Edit `config.local.toml` with your local settings
```toml
# Local Development Configuration Override
GITOPIA_ADDR = "host.docker.internal:9100"
TM_ADDR = "http://host.docker.internal:26667"
CHAIN_ID = "localgitopia"
WORKING_DIR = "/src/app/"

# Local IPFS Configuration
IPFS_CLUSTER_PEER_HOST = "host.docker.internal"
IPFS_HOST = "host.docker.internal"

# Faster cache clearing for development
CACHE_REPO_MAX_AGE = "10m"
CACHE_CLEAR_INTERVAL = "1m"
```

**Step 3**: Run normally - local overrides are automatically loaded
```bash
./gitopia-storaged start
# Output: "Loaded local configuration overrides from config.local.toml"
```

### Docker Deployment

**Production**: Uses `config.toml` in the container
```bash
docker run gitopia-storage
```

**With environment overrides**:
```bash
docker run -e GITOPIA_ADDR="custom-grpc.example.com:9090" gitopia-storage
```

**With custom config volume**:
```bash
docker run -v /host/config.toml:/app/config.toml gitopia-storage
```

### Testing/Staging

**Option 1**: Environment variables
```bash
CHAIN_ID="gitopia-testnet" GITOPIA_ADDR="testnet-grpc.example.com:9090" ./gitopia-storaged start
```

**Option 2**: Create environment-specific local config
```bash
# Create config.staging.toml
cp config.local.toml.example config.staging.toml
# Edit config.staging.toml with staging settings
# Then copy it as config.local.toml when needed
```

## Configuration Files

### config.toml (Base Configuration)
- Contains production defaults
- Always committed to git
- Safe production values
- Should work out-of-the-box for production

### config.local.toml (Development Overrides)
- Gitignored (never committed)
- Overrides specific settings for local development
- Optional - only created when needed
- Automatically loaded if present

### config.local.toml.example (Template)
- Template for local development
- Committed to git as documentation
- Copy to `config.local.toml` and modify as needed

## Environment Variables

Any configuration key can be overridden with environment variables:

```bash
# Override GITOPIA_ADDR
export GITOPIA_ADDR="custom-grpc.example.com:9090"

# Override multiple settings
export CHAIN_ID="custom-chain"
export WEB_SERVER_PORT="8080"
export CACHE_CLEAR_INTERVAL="30m"

./gitopia-storaged start
```

## Troubleshooting

### Config file not found
```
Error reading base config file: Config File "config" Not Found
```
**Solution**: Ensure `config.toml` exists in the current directory or `/etc/gitopia-storage/`

### Local overrides not loading
**Check**: Ensure `config.local.toml` exists and has valid TOML syntax
**Verify**: Look for "Loaded local configuration overrides" message in logs

### Environment variables not working
**Check**: Ensure variable names match config keys exactly (case-sensitive)
**Example**: `GITOPIA_ADDR` not `gitopia_addr`
