package app

import (
	"fmt"

	"github.com/gitopia/gitopia-storage/utils"
	"github.com/gitopia/gitopia/v6/app"
	offchaintypes "github.com/gitopia/gitopia/v6/x/offchain/types"
)

func AuthFunc(token string, req *Request) (bool, error) {
	repoID, err := utils.ParseRepositoryIdfromURI(req.URL.Path)
	if err != nil {
		return false, err
	}

	encConf := app.MakeEncodingConfig()
	offchaintypes.RegisterInterfaces(encConf.InterfaceRegistry)
	offchaintypes.RegisterLegacyAminoCodec(encConf.Amino)

	verifier := offchaintypes.NewVerifier(encConf.TxConfig.SignModeHandler())
	txDecoder := encConf.TxConfig.TxJSONDecoder()

	tx, err := txDecoder([]byte(token))
	if err != nil {
		return false, fmt.Errorf("error decoding token: %w", err)
	}

	msgs := tx.GetMsgs()
	if len(msgs) != 1 || len(msgs[0].GetSigners()) != 1 {
		return false, fmt.Errorf("invalid signature")
	}

	address := msgs[0].GetSigners()[0].String()
	havePushPermission, err := utils.HavePushPermission(repoID, address)
	if err != nil {
		return false, fmt.Errorf("error checking push permission: %w", err)
	}

	if !havePushPermission {
		return false, fmt.Errorf("user does not have push permission")
	}

	if err := verifier.Verify(tx); err != nil {
		return false, fmt.Errorf("signature verification failed: %w", err)
	}

	return true, nil
}
