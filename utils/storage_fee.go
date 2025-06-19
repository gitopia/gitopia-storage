package utils

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	storagetypes "github.com/gitopia/gitopia/v6/x/storage/types"
)

// StorageCostInfo contains information about storage costs and usage
type StorageCostInfo struct {
	StorageCharge sdk.Coin
	CurrentUsage  uint64
	NewUsage      uint64
	FreeLimit     uint64
}

// calculateStorageCost calculates the storage cost based on current usage, new usage, and storage parameters
func CalculateStorageCost(currentUsage, storageDelta uint64, storageParams storagetypes.Params) (*StorageCostInfo, error) {
	freeStorageBytes := storageParams.FreeStorageMb * 1024 * 1024 // Convert MB to bytes
	newUsage := currentUsage + storageDelta

	storageCharge := sdk.NewCoin(storageParams.StoragePricePerMb.Denom, sdk.NewInt(0))
	if currentUsage > freeStorageBytes {
		// If current usage is already above free limit, charge for the entire diff
		if storageDelta > 0 {
			diffMb := float64(storageDelta) / (1024 * 1024)
			chargeAmount := sdk.NewDec(int64(diffMb)).Mul(sdk.NewDecFromInt(storageParams.StoragePricePerMb.Amount))
			storageCharge = sdk.NewCoin(storageParams.StoragePricePerMb.Denom, chargeAmount.TruncateInt())
		}
	} else if newUsage > freeStorageBytes {
		// Calculate charge for the portion that exceeds free limit
		excessBytes := newUsage - freeStorageBytes
		excessMb := float64(excessBytes) / (1024 * 1024)
		chargeAmount := sdk.NewDec(int64(excessMb)).Mul(sdk.NewDecFromInt(storageParams.StoragePricePerMb.Amount))
		storageCharge = sdk.NewCoin(storageParams.StoragePricePerMb.Denom, chargeAmount.TruncateInt())
	}

	return &StorageCostInfo{
		StorageCharge: storageCharge,
		CurrentUsage:  currentUsage,
		NewUsage:      newUsage,
		FreeLimit:     freeStorageBytes,
	}, nil
}
