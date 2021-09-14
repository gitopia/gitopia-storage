package utils

import "encoding/binary"

func UInt64ToBytes(id uint64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, id)
	return bz
}

func BytesToUInt64(bz []byte) uint64 {
	return binary.BigEndian.Uint64(bz)
}
