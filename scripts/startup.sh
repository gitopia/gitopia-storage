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

echo $MNEMONIC | /usr/local/bin/gitopia-storaged keys add gitopia-storage --keyring-backend test --recover
/usr/local/bin/gitopia-storaged register-provider http://gitopia-storage$GIT_SERVER_ID:5000 "test storage provider" 1000000000000ulore --from gitopia-storage --keyring-backend test --fees 200ulore
/usr/local/bin/gitopia-storaged start --from gitopia-storage --keyring-backend test
