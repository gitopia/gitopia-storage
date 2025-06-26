#!/bin/sh

# Exit on error
set -e

# Import the key from the environment variable.
# The variable is mandatory, checked by docker-compose.
echo "$GITOPIA_OPERATOR_MNEMONIC" | gitopia-storaged keys add gitopia-storage --keyring-backend test --recover

export ENV="PRODUCTION"

# Start the main application
exec gitopia-storaged start --from gitopia-storage --keyring-backend test "$@"
