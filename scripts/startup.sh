#! /bin/sh

MNEMONIC="prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
echo $MNEMONIC | /usr/local/bin/git-server-events keys add git-server --keyring-backend test --recover
/usr/local/bin/git-server-events run --from git-server --keyring-backend test
