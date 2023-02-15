package lfs

import (
	"regexp"

	"github.com/pkg/errors"
)

// OID is an LFS object ID.
type OID string

// An OID is a 64-char lower case hexadecimal, produced by SHA256.
// Spec: https://github.com/git-lfs/git-lfs/blob/master/docs/spec.md
const oidRe = "^[a-f0-9]{64}$"

var ErrInvalidOID = errors.New("OID is not valid")

// ValidOID returns true if given oid is valid.
func ValidOID(oid OID) bool {
	matched, _ := regexp.MatchString(oidRe, string(oid))
	return matched
}
