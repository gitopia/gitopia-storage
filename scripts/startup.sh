#! /bin/sh

# Generate a unique mnemonic based on the server ID
case "${GIT_SERVER_ID}" in
  "0")
    MNEMONIC="prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
    ;;
  "1")
    MNEMONIC="torch cry media ladder desert gorilla space safe cross eternal buffalo lock utility salad tree surge drip wreck pear spatial blind inform dice they hospital"
    ;;
  "2")
    MNEMONIC="sunset setup rescue maple cake desert fog nerve science island recall harvest staff short solution menu party gap link access twice theme cotton they shield"
    ;;
  *)
    echo "Invalid GIT_SERVER_ID"
    exit 1
    ;;
esac

MNEMONIC="prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
echo $MNEMONIC | /usr/local/bin/git-server keys add git-server --keyring-backend test --recover
/usr/local/bin/git-server start --from git-server --keyring-backend test
