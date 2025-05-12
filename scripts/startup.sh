#! /bin/sh

# Generate a unique mnemonic based on the server ID
case "${GIT_SERVER_ID}" in
  "0")
    MNEMONIC="prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
    ;;
  "1")
    MNEMONIC="write cricket naive clay differ input vote spell captain smooth interest paddle acquire media ozone invite fish goat holiday village suggest paddle next second"
    ;;
  "2")
    MNEMONIC="manage impulse potato isolate beef brush nominee affair first talk square among toward weasel fame skirt twice face mammal orphan gun trumpet medal flag"
    ;;
  *)
    echo "Invalid GIT_SERVER_ID"
    exit 1
    ;;
esac

echo $MNEMONIC | /usr/local/bin/git-server keys add git-server --keyring-backend test --recover
/usr/local/bin/git-server register-provider http://git-server$GIT_SERVER_ID:5000 10000000000ulore --from git-server --keyring-backend test --fees 200ulore
/usr/local/bin/git-server start --from git-server --keyring-backend test
