package merkleproof

import (
	"fmt"
	"io"
	"os"

	merkletree "github.com/wealdtech/go-merkletree/v2"
)

// PackfileProof represents a Merkle proof for a packfile chunk
type PackfileProof struct {
	// ChunkIndex is the index of the chunk in the packfile
	ChunkIndex uint64
	// ChunkData is the actual data of the chunk
	ChunkData []byte
	// Proof contains the Merkle proof
	Proof *merkletree.Proof
}

// GeneratePackfileProofs creates a Merkle tree from packfile chunks and generates proofs
func GeneratePackfileProofs(chunks [][]byte) ([]PackfileProof, []byte, error) {
	if len(chunks) == 0 {
		return nil, nil, fmt.Errorf("empty chunks array")
	}

	// Create a new Merkle tree with the chunks
	tree, err := merkletree.NewTree(
		merkletree.WithData(chunks),
		merkletree.WithSalt(false), // No salting for simplicity
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create merkle tree: %w", err)
	}

	// Generate proofs for each chunk
	proofs := make([]PackfileProof, len(chunks))
	for i, chunk := range chunks {
		proof, err := tree.GenerateProof(chunk, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate proof for chunk %d: %w", i, err)
		}

		proofs[i] = PackfileProof{
			ChunkIndex: uint64(i),
			ChunkData:  chunk,
			Proof:      proof,
		}
	}

	return proofs, tree.Root(), nil
}

// VerifyPackfileProof verifies a single chunk proof against the Merkle root
func VerifyPackfileProof(proof PackfileProof, root []byte) (bool, error) {
	if proof.Proof == nil {
		return false, fmt.Errorf("nil proof")
	}

	// Verify the proof against the root
	verified, err := merkletree.VerifyProof(
		proof.ChunkData,
		false, // No salting
		proof.Proof,
		[][]byte{root},
	)
	if err != nil {
		return false, fmt.Errorf("failed to verify proof: %w", err)
	}

	return verified, nil
}

// ChunkPackfile reads a packfile and splits it into chunks of equal size
func ChunkPackfile(packfilePath string, chunkSize int) ([][]byte, error) {
	file, err := os.Open(packfilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open packfile: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	fileSize := fileInfo.Size()
	chunks := make([][]byte, 0)

	for offset := int64(0); offset < fileSize; offset += int64(chunkSize) {
		chunk := make([]byte, min(int64(chunkSize), fileSize-offset))
		_, err := file.ReadAt(chunk, offset)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read chunk: %w", err)
		}
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
