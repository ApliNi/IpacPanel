package config

import (
	"IpacPanel/controller/src/msg"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const storedHashPrefix = "HASH/"

const storedPasswordV2Prefix = "P2/"

func IsBcryptHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

func prehashPassword(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	encoded := base64.RawStdEncoding.EncodeToString(sum[:])
	return []byte(encoded)
}

func HashPassword(password string) (string, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return "", errors.New(msg.InvalidPasswordLength)
	}
	if err := ValidateUserPassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword(prehashPassword(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return storedHashPrefix + storedPasswordV2Prefix + string(hash), nil
}

func compareStoredPassword(stored string, password string) bool {
	stored = strings.TrimSpace(stored)
	if !strings.HasPrefix(stored, storedHashPrefix+storedPasswordV2Prefix) {
		return false
	}
	hash := strings.TrimSpace(strings.TrimPrefix(stored, storedHashPrefix+storedPasswordV2Prefix))
	return IsBcryptHash(hash) && bcrypt.CompareHashAndPassword([]byte(hash), prehashPassword(password)) == nil
}

func CheckPassword(stored string, password string) bool {
	return compareStoredPassword(stored, password)
}

func NormalizeAuthUserPasswords(users []AuthUser) (bool, error) {
	changed := false
	for i := range users {
		pass := strings.TrimSpace(users[i].Pass)
		if pass == "" {
			log.Printf(msg.UserEmptyPasswordLoginDisabledLogFmt, strings.TrimSpace(users[i].User))
			continue
		}
		if strings.HasPrefix(pass, storedHashPrefix+storedPasswordV2Prefix) {
			hash := strings.TrimSpace(strings.TrimPrefix(pass, storedHashPrefix+storedPasswordV2Prefix))
			if !IsBcryptHash(hash) {
				return changed, errors.New(msg.InvalidUsernameOrPassword)
			}
			wrapped := storedHashPrefix + storedPasswordV2Prefix + hash
			if users[i].Pass != wrapped {
				users[i].Pass = wrapped
				changed = true
			}
			continue
		}
		if strings.HasPrefix(pass, storedHashPrefix) || strings.HasPrefix(pass, storedPasswordV2Prefix) || IsBcryptHash(pass) {
			return changed, errors.New(msg.InvalidUsernameOrPassword)
		}
		if len([]rune(pass)) > MaxUserPasswordLen {
			log.Printf(msg.UserPlainPasswordTooLongLoginDisabledLogFmt, strings.TrimSpace(users[i].User))
			users[i].Pass = ""
			changed = true
			continue
		}
		stored, err := HashPassword(pass)
		if err != nil {
			return changed, err
		}
		users[i].Pass = stored
		changed = true
	}
	return changed, nil
}

func generateRandomPassword(length int) (string, error) {
	if length <= 0 {
		return "", errors.New(msg.InvalidPasswordLength)
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		out[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(out), nil
}

func EnsureAdminUser() error {
	ManagerMu.Lock()
	if len(CurrentConfig.Auth) > 0 {
		ManagerMu.Unlock()
		return nil
	}
	ManagerMu.Unlock()

	passPlain, err := generateRandomPassword(9)
	if err != nil {
		return err
	}
	stored, err := HashPassword(passPlain)
	if err != nil {
		return err
	}

	ManagerMu.Lock()
	CurrentConfig.Auth = []AuthUser{{
		User: "admin",
		Pass: stored,
		Perm: 7,
	}}
	savedCfg := CloneConfigLocked()
	ManagerMu.Unlock()

	if err := SaveConfigSnapshot(savedCfg); err != nil {
		return err
	}

	log.Printf(msg.InitialAdminUserCreatedLog, passPlain)
	return nil
}
