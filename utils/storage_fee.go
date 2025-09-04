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
// Updated to match the logic from msg_server.go calculateStorageCharge method
func CalculateStorageCost(currentUsage, newUsage uint64, storageParams storagetypes.Params) (*StorageCostInfo, error) {
	freeStorageBytes := storageParams.FreeStorageMb * 1024 * 1024 // Convert MB to bytes

	// If current usage is already above free limit, charge for the entire diff
	if currentUsage > freeStorageBytes {
		diff := newUsage - currentUsage
		if diff <= 0 {
			return &StorageCostInfo{
				StorageCharge: sdk.NewCoin(storageParams.StoragePricePerGb.Denom, sdk.ZeroInt()),
				CurrentUsage:  currentUsage,
				NewUsage:      newUsage,
				FreeLimit:     freeStorageBytes,
			}, nil
		}
		// Calculate charge in GB and multiply by price per GB
		diffGb := float64(diff) / (1024 * 1024 * 1024)
		chargeAmount := sdk.NewDec(int64(diffGb)).Mul(sdk.NewDecFromInt(storageParams.StoragePricePerGb.Amount))
		storageCharge := sdk.NewCoin(storageParams.StoragePricePerGb.Denom, chargeAmount.TruncateInt())
		
		return &StorageCostInfo{
			StorageCharge: storageCharge,
			CurrentUsage:  currentUsage,
			NewUsage:      newUsage,
			FreeLimit:     freeStorageBytes,
		}, nil
	}

	// If new usage is below free limit, no charge
	if newUsage <= freeStorageBytes {
		return &StorageCostInfo{
			StorageCharge: sdk.NewCoin(storageParams.StoragePricePerGb.Denom, sdk.ZeroInt()),
			CurrentUsage:  currentUsage,
			NewUsage:      newUsage,
			FreeLimit:     freeStorageBytes,
		}, nil
	}

	// Calculate charge for the portion that exceeds free limit
	excessBytes := newUsage - freeStorageBytes
	excessGb := float64(excessBytes) / (1024 * 1024 * 1024)
	chargeAmount := sdk.NewDec(int64(excessGb)).Mul(sdk.NewDecFromInt(storageParams.StoragePricePerGb.Amount))
	storageCharge := sdk.NewCoin(storageParams.StoragePricePerGb.Denom, chargeAmount.TruncateInt())

	return &StorageCostInfo{
		StorageCharge: storageCharge,
		CurrentUsage:  currentUsage,
		NewUsage:      newUsage,
		FreeLimit:     freeStorageBytes,
	}, nil
}
