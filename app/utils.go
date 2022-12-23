package app

import (
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// BroadcastTx attempts to generate, sign and broadcast a transaction with the
// given set of messages.
// It will return an error upon failure.
func BroadcastTx(clientCtx client.Context, txf tx.Factory, msgs ...sdk.Msg) (string, error) {
	txf, err := txf.Prepare(clientCtx)
	if err != nil {
		return "", err
	}

	_, adjusted, err := clienttx.CalculateGas(clientCtx, txf, msgs...)
	if err != nil {
		return "", err
	}

	txf = txf.WithGas(adjusted)

	tx, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return "", err
	}

	err = clienttx.Sign(txf, clientCtx.GetFromName(), tx, true)
	if err != nil {
		return "", err
	}

	txBytes, err := clientCtx.TxConfig.TxEncoder()(tx.GetTx())
	if err != nil {
		return "", err
	}

	// broadcast to a Tendermint node
	res, err := clientCtx.BroadcastTx(txBytes)
	if err != nil {
		return "", err
	}

	return res.TxHash, nil
}
