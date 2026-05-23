package authz

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const loginPowSalt = "ipac-login-pow-v1"

type LoginPowChallenge struct {
	Salt      string `json:"salt"`
	Timestamp int64  `json:"timestamp"`
	K         int    `json:"k"`
	D         int    `json:"d"`
}

func GetLoginPowChallenge() *LoginPowChallenge {
	pow := cfg.GetPowConfig()
	if !cfg.IsPowEnabled() {
		return nil
	}
	return &LoginPowChallenge{
		Salt:      loginPowSalt,
		Timestamp: time.Now().UnixMilli(),
		K:         pow.TaskCount,
		D:         pow.Difficulty,
	}
}

func ValidateLoginPow(username string, password string, timestamp int64, nonces []uint64) error {
	pow := cfg.GetPowConfig()
	if !cfg.IsPowEnabled() {
		return nil
	}
	if timestamp <= 0 {
		return fmt.Errorf(msg.PoWVerificationFailed)
	}
	now := time.Now().UnixMilli()
	delta := now - timestamp
	if delta < 0 {
		delta = -delta
	}
	if delta > int64(pow.TimestampMaxSkew)*int64(time.Second/time.Millisecond) {
		return fmt.Errorf(msg.PoWTimestampExpired)
	}
	if len(nonces) != pow.TaskCount {
		return fmt.Errorf(msg.PoWParamsInvalid)
	}

	seed := buildLoginPowSeed(username, password, timestamp)
	prefix := strings.Repeat("4", pow.Difficulty)
	for i, nonce := range nonces {
		powInput := fmt.Sprintf("%s-%d-%d", seed, i, nonce)
		sum := sha256.Sum256([]byte(powInput))
		if !strings.HasPrefix(hex.EncodeToString(sum[:]), prefix) {
			return fmt.Errorf(msg.PoWVerificationFailed)
		}
	}
	return nil
}

func buildLoginPowSeed(username string, password string, timestamp int64) string {
	base := fmt.Sprintf("%s\n%s\n%s\n%d", loginPowSalt, NormalizeUsername(username), password, timestamp)
	sum := sha256.Sum256([]byte(base))
	return hex.EncodeToString(sum[:])
}
