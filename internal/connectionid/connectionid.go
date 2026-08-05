// Package connectionid creates non-semantic, process-independent identifiers
// used only to correlate events from one accepted adaptive connection.
package connectionid

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const Prefix = "conn-"

func New() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate connection ID: %w", err)
	}
	return Prefix + hex.EncodeToString(value), nil
}

func Validate(value string) error {
	if len(value) != len(Prefix)+32 || !strings.HasPrefix(value, Prefix) {
		return errors.New("connection ID must be conn- followed by 32 lowercase hexadecimal characters")
	}
	for _, character := range value[len(Prefix):] {
		if (character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') {
			continue
		}
		return errors.New("connection ID must use lowercase hexadecimal characters")
	}
	return nil
}
