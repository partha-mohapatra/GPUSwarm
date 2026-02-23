package control

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

func ValidatePoW(clientID string, nonce uint64, model string, difficulty int) bool {
	if difficulty <= 0 {
		return true
	}
	s := clientID + ":" + strconv.FormatUint(nonce, 10) + ":" + model
	h := sha256.Sum256([]byte(s))
	hexHash := hex.EncodeToString(h[:])
	if difficulty > len(hexHash) {
		return false
	}
	for i := 0; i < difficulty; i++ {
		if hexHash[i] != '0' {
			return false
		}
	}
	return true
}

func SolvePoW(clientID string, model string, difficulty int, maxIterations uint64) (uint64, bool) {
	if difficulty <= 0 {
		return 0, true
	}
	for i := uint64(0); i < maxIterations; i++ {
		if ValidatePoW(clientID, i, model, difficulty) {
			return i, true
		}
	}
	return 0, false
}
