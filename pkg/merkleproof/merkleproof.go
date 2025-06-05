package merkleproof

import (
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/ipfs/boxo/files"
	merkletree "github.com/wealdtech/go-merkletree/v2"
	"github.com/wealdtech/go-merkletree/v2/sha3"
)

// ComputePackfileMerkleRoot calculates the Merkle root of a packfile by:
// 1. Reading one chunk at a time to minimize memory usage
// 2. Using the hash of each chunk as a leaf node
// 3. Building the Merkle tree only from hashes
func ComputePackfileMerkleRoot(file files.File, chunkSize int) ([]byte, error) {
	// Get file size
	fileSize, err := file.Size()
	if err != nil {
		return nil, fmt.Errorf("failed to get file size: %w", err)
	}

	// Calculate how many chunks we'll have
	numChunks := (fileSize + int64(chunkSize) - 1) / int64(chunkSize)
	if numChunks == 0 {
		return nil, fmt.Errorf("empty file")
	}

	// Pre-compute hashes by reading one chunk at a time
	chunkHashes := make([][]byte, 0, numChunks)
	buffer := make([]byte, chunkSize)
	hasher := sha256.New()

	for offset := int64(0); offset < fileSize; {
		// Calculate actual chunk size (might be smaller for the last chunk)
		currentChunkSize := int(min(int64(chunkSize), fileSize-offset))

		// Reset buffer slice to exact size needed
		chunk := buffer[:currentChunkSize]

		// Read chunk
		n, err := io.ReadFull(file, chunk)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read chunk at offset %d: %w", offset, err)
		}
		if n != currentChunkSize {
			return nil, fmt.Errorf("short read: got %d bytes, expected %d", n, currentChunkSize)
		}

		// Hash the chunk on the fly
		hasher.Reset()
		hasher.Write(chunk)
		hash := hasher.Sum(nil)
		chunkHashes = append(chunkHashes, hash)

		// Move to next chunk
		offset += int64(currentChunkSize)
	}

	// Create Merkle tree from the hashes (directly use hashes as data)
	tree, err := merkletree.NewTree(
		merkletree.WithData(chunkHashes),
		merkletree.WithHashType(sha3.New256()),
		merkletree.WithSalt(false),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create merkle tree: %w", err)
	}

	// Return the root
	return tree.Root(), nil
}

// GenerateChunkProof generates a proof for a specific chunk in the packfile
// This function reads only the chunk needed to generate the proof
func GenerateChunkProof(file files.File, chunkIndex uint64, chunkSize int) (*merkletree.Proof, []byte, []byte, error) {
	fileSize, err := file.Size()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get file size: %w", err)
	}

	// Check if chunk index is valid
	numChunks := (fileSize + int64(chunkSize) - 1) / int64(chunkSize)
	if int64(chunkIndex) >= numChunks {
		return nil, nil, nil, fmt.Errorf("chunk index out of range: %d >= %d", chunkIndex, numChunks)
	}

	// Compute all chunk hashes with a single pass
	chunkHashes := make([][]byte, 0, numChunks)
	buffer := make([]byte, chunkSize)
	hasher := sha256.New()

	for offset := int64(0); offset < fileSize; {
		currentChunkSize := int(min(int64(chunkSize), fileSize-offset))

		// Reset buffer slice to exact size needed
		chunk := buffer[:currentChunkSize]

		// Read chunk
		n, err := io.ReadFull(file, chunk)
		if err != nil && err != io.EOF {
			return nil, nil, nil, fmt.Errorf("failed to read chunk at offset %d: %w", offset, err)
		}
		if n != currentChunkSize {
			return nil, nil, nil, fmt.Errorf("short read: got %d bytes, expected %d", n, currentChunkSize)
		}

		// Hash the chunk on the fly
		hasher.Reset()
		hasher.Write(chunk)
		hash := hasher.Sum(nil)
		chunkHashes = append(chunkHashes, hash)

		// Move to next chunk
		offset += int64(currentChunkSize)
	}

	// Create Merkle tree from the hashes
	tree, err := merkletree.NewTree(
		merkletree.WithData(chunkHashes),
		merkletree.WithHashType(sha3.New256()),
		merkletree.WithSalt(false),
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create merkle tree: %w", err)
	}

	// Generate proof for the target chunk hash
	proof, err := tree.GenerateProof(chunkHashes[chunkIndex], 0)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate proof for chunk %d: %w", chunkIndex, err)
	}

	return proof, tree.Root(), chunkHashes[chunkIndex], nil
}

// VerifyPackfileProof verifies a single chunk proof against the Merkle root
func VerifyPackfileProof(chunkHash []byte, proof *merkletree.Proof, root []byte) (bool, error) {
	if proof == nil {
		return false, fmt.Errorf("nil proof")
	}

	// Verify the proof against the root
	verified, err := merkletree.VerifyProofUsing(
		chunkHash,
		false, // No salting
		proof,
		[][]byte{root},
		sha3.New256(),
	)
	if err != nil {
		return false, fmt.Errorf("failed to verify proof: %w", err)
	}

	return verified, nil
}
