package merkleproof

import (
	"bytes"
	"testing"
)

func TestGenerateAndVerifyPackfileProofs(t *testing.T) {
	tests := []struct {
		name        string
		chunks      [][]byte
		wantErr     bool
		verifyIndex int // Index of chunk to verify
	}{
		{
			name: "valid chunks",
			chunks: [][]byte{
				[]byte("chunk1"),
				[]byte("chunk2"),
				[]byte("chunk3"),
			},
			wantErr:     false,
			verifyIndex: 1,
		},
		{
			name:        "empty chunks",
			chunks:      [][]byte{},
			wantErr:     true,
			verifyIndex: 0,
		},
		{
			name: "single chunk",
			chunks: [][]byte{
				[]byte("chunk1"),
			},
			wantErr:     false,
			verifyIndex: 0,
		},
		{
			name: "large chunks",
			chunks: [][]byte{
				bytes.Repeat([]byte("a"), 1024),
				bytes.Repeat([]byte("b"), 1024),
				bytes.Repeat([]byte("c"), 1024),
				bytes.Repeat([]byte("d"), 1024),
			},
			wantErr:     false,
			verifyIndex: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate proofs
			proofs, root, err := GeneratePackfileProofs(tt.chunks)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("GeneratePackfileProofs() error = %v", err)
				return
			}

			if len(proofs) != len(tt.chunks) {
				t.Errorf("expected %d proofs, got %d", len(tt.chunks), len(proofs))
				return
			}

			// Verify a specific proof
			if len(proofs) > 0 {
				verified, err := VerifyPackfileProof(proofs[tt.verifyIndex], root)
				if err != nil {
					t.Errorf("VerifyPackfileProof() error = %v", err)
					return
				}
				if !verified {
					t.Error("proof verification failed")
				}
			}
		})
	}
}

func TestVerifyPackfileProof_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		proof   PackfileProof
		root    []byte
		wantErr bool
	}{
		{
			name: "nil proof",
			proof: PackfileProof{
				ChunkIndex: 0,
				ChunkData:  []byte("chunk"),
				Proof:      nil,
			},
			root:    []byte("root"),
			wantErr: true,
		},
		{
			name: "empty root",
			proof: PackfileProof{
				ChunkIndex: 0,
				ChunkData:  []byte("chunk"),
				Proof:      nil,
			},
			root:    []byte{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyPackfileProof(tt.proof, tt.root)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyPackfileProof() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
