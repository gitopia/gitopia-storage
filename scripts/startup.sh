#! /bin/sh

# Generate a unique mnemonic based on the server ID
case "${GIT_SERVER_ID}" in
  "0")
    MNEMONIC="prize cycle gravity trumpet force initial print pulp correct maze mechanic what gallery debris ice announce chunk curtain gate deliver walk resist forest grid"
    API_URL="http://localhost:5001"
    MONIKER="test-storage-provider-0"
    ;;
  "1")
    MNEMONIC="write cricket naive clay differ input vote spell captain smooth interest paddle acquire media ozone invite fish goat holiday village suggest paddle next second"
    API_URL="http://localhost:5002"
    MONIKER="test-storage-provider-1"
    ;;
  "2")
    MNEMONIC="manage impulse potato isolate beef brush nominee affair first talk square among toward weasel fame skirt twice face mammal orphan gun trumpet medal flag"
    API_URL="http://localhost:5003"
    MONIKER="test-storage-provider-2"
    ;;
  *)
    echo "Invalid GIT_SERVER_ID"
    exit 1
    ;;
esac

echo $MNEMONIC | /usr/local/bin/gitopia-storaged keys add gitopia-storage --keyring-backend test --recover
/usr/local/bin/gitopia-storaged register-provider $API_URL $MONIKER 1000000000000ulore "/ip4/104.244.178.22/tcp/9096/p2p/12D3KooWHWG333xgo3QnZTSP9trGmGBvs37JsqwiUz5XYHjMpgfA" --from gitopia-storage --keyring-backend test --fees 200ulore
/usr/local/bin/gitopia-storaged start --from gitopia-storage --keyring-backend test
