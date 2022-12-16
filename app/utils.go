package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/input"
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

	if txf.SimulateAndExecute() || clientCtx.Simulate {
		_, adjusted, err := clienttx.CalculateGas(clientCtx, txf, msgs...)
		if err != nil {
			return "", err
		}

		txf = txf.WithGas(adjusted)
		// _, _ = fmt.Fprintf(os.Stderr, "%s\n", tx.GasEstimateResponse{GasEstimate: txf.Gas()})
	}

	tx, err := txf.BuildUnsignedTx(msgs...)
	if err != nil {
		return "", err
	}

	if !clientCtx.SkipConfirm {
		txBytes, err := clientCtx.TxConfig.TxJSONEncoder()(tx.GetTx())
		if err != nil {
			return "", err
		}

		if err := clientCtx.PrintRaw(json.RawMessage(txBytes)); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "%s\n", txBytes)
		}

		buf := bufio.NewReader(os.Stdin)
		ok, err := input.GetConfirmation("confirm transaction before signing and broadcasting", buf, os.Stderr)

		if err != nil || !ok {
			// _, _ = fmt.Fprintf(os.Stderr, "%s\n", "cancelled transaction")
			return "", err
		}
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
